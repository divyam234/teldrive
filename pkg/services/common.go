package services

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"sync"

	"github.com/divyam234/teldrive/internal/cache"
	"github.com/divyam234/teldrive/internal/config"
	"github.com/divyam234/teldrive/internal/crypt"
	"github.com/divyam234/teldrive/internal/kv"
	"github.com/divyam234/teldrive/internal/tgc"
	"github.com/divyam234/teldrive/pkg/models"
	"github.com/divyam234/teldrive/pkg/schemas"
	"github.com/divyam234/teldrive/pkg/types"
	"github.com/gin-gonic/gin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/pkg/errors"
	"github.com/thoas/go-funk"
	"golang.org/x/sync/errgroup"
	"gorm.io/gorm"
)

type buffer struct {
	Buf []byte
}

func (b *buffer) long() (int64, error) {
	v, err := b.uint64()
	if err != nil {
		return 0, err
	}
	return int64(v), nil
}
func (b *buffer) uint64() (uint64, error) {
	const size = 8
	if len(b.Buf) < size {
		return 0, io.ErrUnexpectedEOF
	}
	v := binary.LittleEndian.Uint64(b.Buf)
	b.Buf = b.Buf[size:]
	return v, nil
}

func randInt64() (int64, error) {
	var buf [8]byte
	if _, err := io.ReadFull(rand.Reader, buf[:]); err != nil {
		return 0, err
	}
	b := &buffer{Buf: buf[:]}
	return b.long()
}

type batchResult struct {
	Index    int
	Messages *tg.MessagesChannelMessages
}

func getChunk(ctx context.Context, tgClient *telegram.Client, location tg.InputFileLocationClass, offset int64, limit int64) ([]byte, error) {

	req := &tg.UploadGetFileRequest{
		Offset:   offset,
		Limit:    int(limit),
		Location: location,
	}

	r, err := tgClient.API().UploadGetFile(ctx, req)

	if err != nil {
		return nil, err
	}

	switch result := r.(type) {
	case *tg.UploadFile:
		return result.Bytes, nil
	default:
		return nil, fmt.Errorf("unexpected type %T", r)
	}
}

func iterContent(ctx context.Context, tgClient *telegram.Client, location tg.InputFileLocationClass) (*bytes.Buffer, error) {
	offset := int64(0)
	limit := int64(1024 * 1024)
	buff := &bytes.Buffer{}
	for {
		r, err := getChunk(ctx, tgClient, location, offset, limit)
		if err != nil {
			return buff, err
		}
		if len(r) == 0 {
			break
		}
		buff.Write(r)
		offset += int64(limit)
	}
	return buff, nil
}

func GetUserAuth(c *gin.Context) (int64, string) {
	val, _ := c.Get("jwtUser")
	jwtUser := val.(*types.JWTClaims)
	userId, _ := strconv.ParseInt(jwtUser.Subject, 10, 64)
	return userId, jwtUser.TgSession
}

func getBotInfo(ctx context.Context, KV kv.KV, config *config.TGConfig, token string) (*types.BotInfo, error) {
	client, _ := tgc.BotClient(ctx, KV, config, token, tgc.Middlewares(config, 5)...)
	var user *tg.User
	err := tgc.RunWithAuth(ctx, client, token, func(ctx context.Context) error {
		user, _ = client.Self(ctx)
		return nil
	})

	if err != nil {
		return nil, err
	}
	return &types.BotInfo{Id: user.ID, UserName: user.Username, Token: token}, nil
}

func getTGMessagesBatch(ctx context.Context, client *telegram.Client, channel *tg.InputChannel, parts []schemas.Part, index int,
	results chan<- batchResult, errors chan<- error, wg *sync.WaitGroup) {

	defer wg.Done()

	ids := funk.Map(parts, func(part schemas.Part) tg.InputMessageClass {
		return &tg.InputMessageID{ID: int(part.ID)}
	}).([]tg.InputMessageClass)

	messageRequest := tg.ChannelsGetMessagesRequest{
		Channel: channel,
		ID:      ids,
	}

	res, err := client.API().ChannelsGetMessages(ctx, &messageRequest)
	if err != nil {
		errors <- err
		return
	}

	messages, ok := res.(*tg.MessagesChannelMessages)

	if !ok {
		errors <- fmt.Errorf("unexpected response type: %T", res)
		return
	}

	results <- batchResult{Index: index, Messages: messages}
}

func getTGMessages(ctx context.Context, client *telegram.Client, parts []schemas.Part, channelId int64, userID string) ([]tg.MessageClass, error) {

	channel, err := GetChannelById(ctx, client, channelId, userID)

	if err != nil {
		return nil, err
	}

	var wg sync.WaitGroup

	batchSize := 200

	batchCount := int(math.Ceil(float64(len(parts)) / float64(batchSize)))

	results := make(chan batchResult, batchCount)

	errors := make(chan error, batchCount)

	for i := range batchCount {
		wg.Add(1)
		splitParts := parts[i*batchSize : min((i+1)*batchSize, len(parts))]
		go getTGMessagesBatch(ctx, client, channel, splitParts, i, results, errors, &wg)
	}

	wg.Wait()
	close(results)
	close(errors)

	for err := range errors {
		if err != nil {
			return nil, err
		}
	}

	batchResults := []batchResult{}

	for result := range results {
		batchResults = append(batchResults, result)
	}

	sort.Slice(batchResults, func(i, j int) bool {
		return batchResults[i].Index < batchResults[j].Index
	})

	allMessages := []tg.MessageClass{}

	for _, result := range batchResults {
		allMessages = append(allMessages, result.Messages.GetMessages()...)
	}

	return allMessages, nil
}

func getParts(ctx context.Context, client *telegram.Client, file *schemas.FileOutFull, userID string) ([]types.Part, error) {
	cache := cache.FromContext(ctx)
	parts := []types.Part{}

	key := fmt.Sprintf("messages:%s:%s", file.Id, userID)

	err := cache.Get(key, &parts)

	if err == nil {
		return parts, nil
	}

	messages, err := getTGMessages(ctx, client, file.Parts, file.ChannelID, userID)

	if err != nil {
		return nil, err
	}

	for i, message := range messages {
		item := message.(*tg.Message)
		media := item.Media.(*tg.MessageMediaDocument)
		document := media.Document.(*tg.Document)

		part := types.Part{
			ID:   file.Parts[i].ID,
			Size: document.Size,
			Salt: file.Parts[i].Salt,
		}
		if file.Encrypted {
			part.DecryptedSize, _ = crypt.DecryptedSize(document.Size)
		}
		parts = append(parts, part)
	}
	cache.Set(key, &parts, 3600)
	return parts, nil
}

func GetChannelById(ctx context.Context, client *telegram.Client, channelId int64, userID string) (*tg.InputChannel, error) {

	channel := &tg.InputChannel{}
	inputChannel := &tg.InputChannel{
		ChannelID: channelId,
	}
	channels, err := client.API().ChannelsGetChannels(ctx, []tg.InputChannelClass{inputChannel})

	if err != nil {
		return nil, err
	}

	if len(channels.GetChats()) == 0 {
		return nil, errors.New("no channels found")
	}

	channel = channels.GetChats()[0].(*tg.Channel).AsInput()
	return channel, nil
}

func GetDefaultChannel(ctx context.Context, db *gorm.DB, userID int64) (int64, error) {
	cache := cache.FromContext(ctx)
	var channelId int64
	key := fmt.Sprintf("users:channel:%d", userID)

	err := cache.Get(key, &channelId)

	if err == nil {
		return channelId, nil
	}

	var channelIds []int64
	db.Model(&models.Channel{}).Where("user_id = ?", userID).Where("selected = ?", true).
		Pluck("channel_id", &channelIds)

	if len(channelIds) == 1 {
		channelId = channelIds[0]
		cache.Set(key, channelId, 0)
	}

	if channelId == 0 {
		return channelId, errors.New("default channel not set")
	}

	return channelId, nil
}

func getBotsToken(ctx context.Context, db *gorm.DB, userID, channelId int64) ([]string, error) {
	cache := cache.FromContext(ctx)
	var bots []string

	key := fmt.Sprintf("users:bots:%d:%d", userID, channelId)

	err := cache.Get(key, &bots)

	if err == nil {
		return bots, nil
	}

	if err := db.Model(&models.Bot{}).Where("user_id = ?", userID).
		Where("channel_id = ?", channelId).Pluck("token", &bots).Error; err != nil {
		return nil, err
	}

	cache.Set(key, &bots, 0)
	return bots, nil

}

func getSessionByHash(db *gorm.DB, cache *cache.Cache, hash string) (*models.Session, error) {
	var session models.Session

	key := fmt.Sprintf("sessions:%s", hash)

	err := cache.Get(key, &session)

	if err != nil {
		if err := db.Model(&models.Session{}).Where("hash = ?", hash).First(&session).Error; err != nil {
			return nil, err
		}
		cache.Set(key, &session, 0)
	}

	return &session, nil

}

func DeleteTGMessages(ctx context.Context, cnf *config.TGConfig, session string, channelId, userId int64, ids []int) error {

	client, _ := tgc.AuthClient(ctx, cnf, session)

	err := tgc.RunWithAuth(ctx, client, "", func(ctx context.Context) error {
		channel, err := GetChannelById(ctx, client, channelId, strconv.FormatInt(userId, 10))

		if err != nil {
			return err
		}

		batchSize := 100

		batchCount := int(math.Ceil(float64(len(ids)) / float64(batchSize)))

		g, _ := errgroup.WithContext(ctx)

		g.SetLimit(8)

		for i := 0; i < batchCount; i++ {
			start := i * batchSize
			end := min((i+1)*batchSize, len(ids))
			batchIds := ids[start:end]
			g.Go(func() error {
				messageDeleteRequest := tg.ChannelsDeleteMessagesRequest{Channel: channel, ID: batchIds}
				_, err = client.API().ChannelsDeleteMessages(ctx, &messageDeleteRequest)
				return err
			})
		}
		return g.Wait()
	})
	return err
}
