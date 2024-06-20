package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/divyam234/teldrive/internal/auth"
	"github.com/divyam234/teldrive/internal/cache"
	"github.com/divyam234/teldrive/internal/config"
	"github.com/divyam234/teldrive/internal/kv"
	"github.com/divyam234/teldrive/internal/logging"
	"github.com/divyam234/teldrive/internal/tgc"
	"github.com/divyam234/teldrive/pkg/models"
	"github.com/divyam234/teldrive/pkg/schemas"
	"github.com/divyam234/teldrive/pkg/types"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/thoas/go-funk"
	"go.uber.org/zap"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type UserService struct {
	db  *gorm.DB
	cnf *config.Config
	kv  kv.KV
}

func NewUserService(db *gorm.DB, cnf *config.Config, kv kv.KV) *UserService {
	return &UserService{db: db, cnf: cnf, kv: kv}
}
func (us *UserService) GetProfilePhoto(c *gin.Context) {
	_, session := auth.GetUser(c)

	client, err := tgc.AuthClient(c, &us.cnf.TG, session)

	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	err = tgc.RunWithAuth(c, client, "", func(ctx context.Context) error {
		self, err := client.Self(c)
		if err != nil {
			return err
		}
		peer := self.AsInputPeer()
		if self.Photo == nil {
			return nil
		}
		photo, ok := self.Photo.AsNotEmpty()
		if !ok {
			return errors.New("profile not found")
		}
		location := &tg.InputPeerPhotoFileLocation{Big: false, Peer: peer, PhotoID: photo.PhotoID}
		buff, err := tgc.GetMediaContent(c, client.API(), location)
		if err != nil {
			return err
		}
		content := buff.Bytes()
		c.Writer.Header().Set("Content-Type", "image/jpeg")
		c.Writer.Header().Set("Cache-Control", "public, max-age=86400")
		c.Writer.Header().Set("Content-Length", strconv.Itoa(len(content)))
		c.Writer.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", "profile.jpeg"))
		c.Writer.Write(content)
		return nil
	})
	if err != nil {
		c.AbortWithError(http.StatusNotFound, err)
		return
	}
}

func (us *UserService) GetStats(c *gin.Context) (*schemas.AccountStats, *types.AppError) {
	userID, _ := auth.GetUser(c)
	var (
		channelId int64
		err       error
	)

	channelId, _ = getDefaultChannel(c, us.db, userID)

	tokens, err := getBotsToken(c, us.db, userID, channelId)

	if err != nil {
		return nil, &types.AppError{Error: err, Code: http.StatusInternalServerError}
	}
	return &schemas.AccountStats{Bots: tokens, ChannelID: channelId}, nil
}

func (us *UserService) UpdateChannel(c *gin.Context) (*schemas.Message, *types.AppError) {

	cache := cache.FromContext(c)

	userId, _ := auth.GetUser(c)

	var payload schemas.Channel

	if err := c.ShouldBindJSON(&payload); err != nil {
		return nil, &types.AppError{Error: err, Code: http.StatusBadRequest}
	}

	channel := &models.Channel{ChannelID: payload.ChannelID, ChannelName: payload.ChannelName, UserID: userId,
		Selected: true}

	if err := us.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "channel_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{"selected": true}),
	}).Create(channel).Error; err != nil {
		return nil, &types.AppError{Error: errors.New("failed to update channel"),
			Code: http.StatusInternalServerError}
	}
	us.db.Model(&models.Channel{}).Where("channel_id != ?", payload.ChannelID).
		Where("user_id = ?", userId).Update("selected", false)

	key := fmt.Sprintf("users:channel:%d", userId)
	cache.Set(key, payload.ChannelID, 0)
	return &schemas.Message{Message: "channel updated"}, nil
}

func (us *UserService) ListSessions(c *gin.Context) ([]schemas.SessionOut, *types.AppError) {
	userId, userSession := auth.GetUser(c)

	client, _ := tgc.AuthClient(c, &us.cnf.TG, userSession)

	var (
		auth *tg.AccountAuthorizations
		err  error
	)

	err = client.Run(c, func(ctx context.Context) error {
		auth, err = client.API().AccountGetAuthorizations(c)
		if err != nil {
			return err
		}
		return nil
	})

	if err != nil && !tgerr.Is(err, "AUTH_KEY_UNREGISTERED") {
		return nil, &types.AppError{Error: err}
	}

	dbSessions := []models.Session{}

	if err = us.db.Where("user_id = ?", userId).Order("created_at DESC").Find(&dbSessions).Error; err != nil {
		return nil, &types.AppError{Error: err}
	}

	sessionsOut := []schemas.SessionOut{}

	for _, session := range dbSessions {

		s := schemas.SessionOut{Hash: session.Hash,
			CreatedAt: session.CreatedAt.UTC().Format(time.RFC3339),
			Current:   session.Session == userSession}

		if auth != nil {
			for _, a := range auth.Authorizations {
				if session.SessionDate == a.DateCreated {
					s.AppName = strings.Trim(strings.Replace(a.AppName, "Telegram", "", -1), " ")
					s.Location = a.Country
					s.OfficialApp = a.OfficialApp
					s.Valid = true
					break
				}
			}
		}

		sessionsOut = append(sessionsOut, s)
	}

	return sessionsOut, nil
}

func (us *UserService) RemoveSession(c *gin.Context) (*schemas.Message, *types.AppError) {

	userId, _ := auth.GetUser(c)

	session := &models.Session{}

	if err := us.db.Where("user_id = ?", userId).Where("hash = ?", c.Param("id")).First(session).Error; err != nil {
		return nil, &types.AppError{Error: err}
	}

	client, _ := tgc.AuthClient(c, &us.cnf.TG, session.Session)

	client.Run(c, func(ctx context.Context) error {
		_, err := client.API().AuthLogOut(c)
		if err != nil {
			return err
		}
		return nil
	})

	us.db.Where("user_id = ?", userId).Where("hash = ?", session.Hash).Delete(&models.Session{})

	return &schemas.Message{Message: "session deleted"}, nil
}

func (us *UserService) ListChannels(c *gin.Context) (interface{}, *types.AppError) {
	_, session := auth.GetUser(c)
	client, _ := tgc.AuthClient(c, &us.cnf.TG, session)

	channels := make(map[int64]*schemas.Channel)

	client.Run(c, func(ctx context.Context) error {

		dialogs, _ := query.GetDialogs(client.API()).BatchSize(100).Collect(ctx)

		for _, dialog := range dialogs {
			if !dialog.Deleted() {
				for _, channel := range dialog.Entities.Channels() {
					_, exists := channels[channel.ID]
					if !exists && channel.AdminRights.AddAdmins {
						channels[channel.ID] = &schemas.Channel{ChannelID: channel.ID, ChannelName: channel.Title}
					}
				}
			}
		}
		return nil

	})

	return funk.Values(channels), nil
}

func (us *UserService) AddBots(c *gin.Context) (*schemas.Message, *types.AppError) {
	userId, session := auth.GetUser(c)
	client, _ := tgc.AuthClient(c, &us.cnf.TG, session)

	var botsTokens []string

	if err := c.ShouldBindJSON(&botsTokens); err != nil {
		return nil, &types.AppError{Error: err, Code: http.StatusBadRequest}
	}

	if len(botsTokens) == 0 {
		return &schemas.Message{Message: "no bots to add"}, nil
	}

	channelId, err := getDefaultChannel(c, us.db, userId)

	if err != nil {
		return nil, &types.AppError{Error: err, Code: http.StatusInternalServerError}
	}

	return us.addBots(c, client, userId, channelId, botsTokens)

}

func (us *UserService) RemoveBots(c *gin.Context) (*schemas.Message, *types.AppError) {

	cache := cache.FromContext(c)

	userID, _ := auth.GetUser(c)

	channelId, err := getDefaultChannel(c, us.db, userID)

	if err != nil {
		return nil, &types.AppError{Error: err, Code: http.StatusInternalServerError}
	}

	if err := us.db.Where("user_id = ?", userID).Where("channel_id = ?", channelId).
		Delete(&models.Bot{}).Error; err != nil {
		return nil, &types.AppError{Error: err, Code: http.StatusInternalServerError}
	}

	cache.Delete(fmt.Sprintf("users:bots:%d:%d", userID, channelId))

	return &schemas.Message{Message: "bots deleted"}, nil

}

func (us *UserService) addBots(c context.Context, client *telegram.Client, userId int64, channelId int64, botsTokens []string) (*schemas.Message, *types.AppError) {

	botInfo := []types.BotInfo{}

	var wg sync.WaitGroup

	logger := logging.FromContext(c)

	cache := cache.FromContext(c)

	err := tgc.RunWithAuth(c, client, "", func(ctx context.Context) error {

		channel, err := tgc.GetChannelById(ctx, client.API(), channelId)

		if err != nil {
			logger.Error("error", zap.Error(err))
			return err
		}

		botInfoChannel := make(chan *types.BotInfo, len(botsTokens))

		waitChan := make(chan struct{}, 6)

		for _, token := range botsTokens {
			waitChan <- struct{}{}
			wg.Add(1)
			go func(t string) {
				info, err := tgc.GetBotInfo(c, client, t)
				if err != nil {
					return
				}
				botPeerClass, err := peer.DefaultResolver(client.API()).ResolveDomain(ctx, info.UserName)
				if err != nil {
					logger.Error("error", zap.Error(err))
					return
				}
				botPeer := botPeerClass.(*tg.InputPeerUser)
				info.AccessHash = botPeer.AccessHash
				defer func() {
					<-waitChan
					wg.Done()
				}()

				botInfoChannel <- info

			}(token)
		}

		wg.Wait()
		close(botInfoChannel)
		for result := range botInfoChannel {
			botInfo = append(botInfo, *result)
		}

		if len(botsTokens) == len(botInfo) {
			users := funk.Map(botInfo, func(info types.BotInfo) tg.InputUser {
				return tg.InputUser{UserID: info.Id, AccessHash: info.AccessHash}
			})
			botsToAdd := users.([]tg.InputUser)
			for _, user := range botsToAdd {
				payload := &tg.ChannelsEditAdminRequest{
					Channel: channel,
					UserID:  tg.InputUserClass(&user),
					AdminRights: tg.ChatAdminRights{
						ChangeInfo:     true,
						PostMessages:   true,
						EditMessages:   true,
						DeleteMessages: true,
						BanUsers:       true,
						InviteUsers:    true,
						PinMessages:    true,
						ManageCall:     true,
						Other:          true,
						ManageTopics:   true,
					},
					Rank: "bot",
				}
				_, err := client.API().ChannelsEditAdmin(ctx, payload)
				if err != nil {
					logger.Error("error", zap.Error(err))
					return err
				}

			}
		} else {
			return errors.New("failed to fetch bots")
		}
		return nil
	})

	if err != nil {
		return nil, &types.AppError{Error: err, Code: http.StatusInternalServerError}
	}

	payload := []models.Bot{}

	for _, info := range botInfo {
		payload = append(payload, models.Bot{UserID: userId, Token: info.Token, BotID: info.Id,
			BotUserName: info.UserName, ChannelID: channelId,
		})
	}

	cache.Delete(fmt.Sprintf("users:bots:%d:%d", userId, channelId))

	if err := us.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&payload).Error; err != nil {
		return nil, &types.AppError{Error: err, Code: http.StatusInternalServerError}
	}

	return &schemas.Message{Message: "bots added"}, nil

}
