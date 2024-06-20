package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/divyam234/teldrive/internal/auth"
	"github.com/divyam234/teldrive/internal/crypt"
	"github.com/divyam234/teldrive/internal/kv"
	"github.com/divyam234/teldrive/internal/logging"
	"github.com/divyam234/teldrive/internal/pool"
	"github.com/divyam234/teldrive/internal/tgc"
	"github.com/divyam234/teldrive/pkg/mapper"
	"github.com/divyam234/teldrive/pkg/schemas"

	"github.com/divyam234/teldrive/pkg/types"

	"github.com/divyam234/teldrive/internal/config"
	"github.com/divyam234/teldrive/pkg/models"
	"github.com/gin-gonic/gin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"gorm.io/gorm"
)

const saltLength = 32

type UploadService struct {
	db     *gorm.DB
	worker *tgc.UploadWorker
	cnf    *config.TGConfig
	kv     kv.KV
}

func NewUploadService(db *gorm.DB, cnf *config.Config, worker *tgc.UploadWorker, kv kv.KV) *UploadService {
	return &UploadService{db: db, worker: worker, cnf: &cnf.TG, kv: kv}
}

func (us *UploadService) GetUploadFileById(c *gin.Context) (*schemas.UploadOut, *types.AppError) {
	uploadId := c.Param("id")
	parts := []schemas.UploadPartOut{}
	if err := us.db.Model(&models.Upload{}).Order("part_no").Where("upload_id = ?", uploadId).
		Where("created_at < ?", time.Now().UTC().Add(us.cnf.Uploads.Retention)).
		Find(&parts).Error; err != nil {
		return nil, &types.AppError{Error: err}
	}

	return &schemas.UploadOut{Parts: parts}, nil
}

func (us *UploadService) DeleteUploadFile(c *gin.Context) (*schemas.Message, *types.AppError) {
	uploadId := c.Param("id")
	if err := us.db.Where("upload_id = ?", uploadId).Delete(&models.Upload{}).Error; err != nil {
		return nil, &types.AppError{Error: err}
	}
	return &schemas.Message{Message: "upload deleted"}, nil
}

func (us *UploadService) GetUploadStats(userId int64, days int) ([]schemas.UploadStats, *types.AppError) {
	var stats []schemas.UploadStats
	err := us.db.Raw(`
    SELECT 
        dates.upload_date::date AS upload_date,
        COALESCE(SUM(files.size), 0)::bigint AS total_uploaded
    FROM 
        generate_series(CURRENT_DATE - INTERVAL '1 day' * @days, CURRENT_DATE, '1 day') AS dates(upload_date)
    LEFT JOIN 
        teldrive.files AS files
    ON 
        dates.upload_date = DATE_TRUNC('day', files.created_at)
    WHERE 
	    dates.upload_date >= CURRENT_DATE - INTERVAL '1 day' * @days and (files.type='file' or files.type is null) and (files.user_id=@userId or files.user_id is null)
    GROUP BY 
        dates.upload_date
    ORDER BY 
        dates.upload_date
  `, sql.Named("days", days-1), sql.Named("userId", userId)).Scan(&stats).Error

	if err != nil {
		return nil, &types.AppError{Error: err}

	}

	return stats, nil
}

func (us *UploadService) UploadFile(c *gin.Context) (*schemas.UploadPartOut, *types.AppError) {
	var (
		uploadQuery schemas.UploadQuery
		channelId   int64
		err         error
		client      *telegram.Client
		middlewares []telegram.Middleware
		token       string
		index       int
		channelUser string
		out         *schemas.UploadPartOut
	)

	if err := c.ShouldBindQuery(&uploadQuery); err != nil {
		return nil, &types.AppError{Error: err, Code: http.StatusBadRequest}
	}

	if uploadQuery.Encrypted && us.cnf.Uploads.EncryptionKey == "" {
		return nil, &types.AppError{Error: errors.New("encryption key not found"),
			Code: http.StatusBadRequest}
	}

	userId, session := auth.GetUser(c)

	uploadId := c.Param("id")

	fileStream := c.Request.Body

	fileSize := c.Request.ContentLength

	defer fileStream.Close()

	if uploadQuery.ChannelID == 0 {
		channelId, err = getDefaultChannel(c, us.db, userId)
		if err != nil {
			return nil, &types.AppError{Error: err}
		}
	} else {
		channelId = uploadQuery.ChannelID
	}

	tokens, err := getBotsToken(c, us.db, userId, channelId)

	if err != nil {
		return nil, &types.AppError{Error: err}
	}

	if len(tokens) == 0 {
		client, err = tgc.AuthClient(c, us.cnf, session)
		if err != nil {
			return nil, &types.AppError{Error: err}
		}
		channelUser = strconv.FormatInt(userId, 10)
	} else {
		us.worker.Set(tokens, channelId)
		token, index = us.worker.Next(channelId)
		client, err = tgc.BotClient(c, us.kv, us.cnf, token)

		if err != nil {
			return nil, &types.AppError{Error: err}
		}

		channelUser = strings.Split(token, ":")[0]
	}

	middlewares = tgc.Middlewares(us.cnf, us.cnf.Uploads.MaxRetries)

	uploadPool := pool.NewPool(client, int64(us.cnf.PoolSize), middlewares...)

	defer uploadPool.Close()

	logger := logging.FromContext(c)

	logger.Debugw("uploading chunk", "fileName", uploadQuery.FileName,
		"partName", uploadQuery.PartName,
		"bot", channelUser, "botNo", index,
		"chunkNo", uploadQuery.PartNo, "partSize", fileSize)

	err = tgc.RunWithAuth(c, client, token, func(ctx context.Context) error {

		channel, err := tgc.GetChannelById(ctx, client.API(), channelId)

		if err != nil {
			return err
		}

		var salt string

		if uploadQuery.Encrypted {
			//gen random Salt
			salt, _ = generateRandomSalt()
			cipher, _ := crypt.NewCipher(us.cnf.Uploads.EncryptionKey, salt)
			fileSize = crypt.EncryptedSize(fileSize)
			fileStream, _ = cipher.EncryptData(fileStream)
		}

		client := uploadPool.Default(ctx)

		u := uploader.NewUploader(client).WithThreads(us.cnf.Uploads.Threads).WithPartSize(512 * 1024)

		upload, err := u.Upload(ctx, uploader.NewUpload(uploadQuery.PartName, fileStream, fileSize))

		if err != nil {
			return err
		}

		document := message.UploadedDocument(upload).Filename(uploadQuery.PartName).ForceFile(true)

		sender := message.NewSender(client)

		target := sender.To(&tg.InputPeerChannel{ChannelID: channel.ChannelID,
			AccessHash: channel.AccessHash})

		res, err := target.Media(ctx, document)

		if err != nil {
			return err
		}

		updates := res.(*tg.Updates)

		var message *tg.Message

		for _, update := range updates.Updates {
			channelMsg, ok := update.(*tg.UpdateNewChannelMessage)
			if ok {
				message = channelMsg.Message.(*tg.Message)
				break
			}
		}

		if message.ID == 0 {
			return fmt.Errorf("upload failed")
		}

		partUpload := &models.Upload{
			Name:      uploadQuery.PartName,
			UploadId:  uploadId,
			PartId:    message.ID,
			ChannelID: channelId,
			Size:      fileSize,
			PartNo:    uploadQuery.PartNo,
			UserId:    userId,
			Encrypted: uploadQuery.Encrypted,
			Salt:      salt,
		}

		if err := us.db.Create(partUpload).Error; err != nil {
			if message.ID != 0 {
				client.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{Channel: channel, ID: []int{message.ID}})
			}
			return err
		}

		out = mapper.ToUploadOut(partUpload)

		return nil
	})

	if err != nil {
		logger.Debugw("upload failed", "fileName", uploadQuery.FileName,
			"partName", uploadQuery.PartName,
			"chunkNo", uploadQuery.PartNo)
		return nil, &types.AppError{Error: err}
	}
	logger.Debugw("upload finished", "fileName", uploadQuery.FileName,
		"partName", uploadQuery.PartName,
		"chunkNo", uploadQuery.PartNo)
	return out, nil

}

func generateRandomSalt() (string, error) {
	randomBytes := make([]byte, saltLength)
	_, err := rand.Read(randomBytes)
	if err != nil {
		return "", err
	}

	hasher := sha256.New()
	hasher.Write(randomBytes)
	hashedSalt := base64.URLEncoding.EncodeToString(hasher.Sum(nil))

	return hashedSalt, nil
}
