package tgc

import (
	"context"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/divyam234/teldrive/database"
	"github.com/divyam234/teldrive/utils"
	"github.com/divyam234/teldrive/utils/kv"
	"github.com/gotd/contrib/middleware/floodwait"
	"github.com/gotd/contrib/middleware/ratelimit"
	tdclock "github.com/gotd/td/clock"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"golang.org/x/time/rate"
)

func deviceConfig(appConfig *utils.Config) telegram.DeviceConfig {
	config := telegram.DeviceConfig{
		DeviceModel:    appConfig.TgClientDeviceModel,
		SystemVersion:  appConfig.TgClientSystemVersion,
		AppVersion:     appConfig.TgClientAppVersion,
		SystemLangCode: appConfig.TgClientSystemLangCode,
		LangPack:       appConfig.TgClientLangPack,
		LangCode:       appConfig.TgClientLangCode,
	}
	return config
}

func New(handler telegram.UpdateHandler, storage session.Storage, middlewares ...telegram.Middleware) *telegram.Client {

	_clock := tdclock.System

	config := utils.GetConfig()

	noUpdates := true

	if handler != nil {
		noUpdates = false
	}

	opts := telegram.Options{
		ReconnectionBackoff: func() backoff.BackOff {
			b := backoff.NewExponentialBackOff()

			b.Multiplier = 1.1
			b.MaxElapsedTime = time.Duration(120) * time.Second
			b.Clock = _clock
			return b
		},
		Device:         deviceConfig(config),
		SessionStorage: storage,
		RetryInterval:  5 * time.Second,
		MaxRetries:     5,
		DialTimeout:    10 * time.Second,
		Middlewares:    middlewares,
		Clock:          _clock,
		NoUpdates:      noUpdates,
		UpdateHandler:  handler,
	}

	return telegram.NewClient(config.AppId, config.AppHash, opts)
}

func NoLogin(handler telegram.UpdateHandler, storage session.Storage) *telegram.Client {
	middlewares := []telegram.Middleware{floodwait.NewSimpleWaiter()}
	middlewares = append(middlewares, ratelimit.New(rate.Every(time.Millisecond*100), 5))
	return New(handler, storage, middlewares...)
}

func UserLogin(sessionStr string) (*telegram.Client, error) {
	data, err := session.TelethonSession(sessionStr)

	if err != nil {
		return nil, err
	}

	var (
		storage = new(session.StorageMemory)
		loader  = session.Loader{Storage: storage}
	)

	if err := loader.Save(context.TODO(), data); err != nil {
		return nil, err
	}
	middlewares := []telegram.Middleware{floodwait.NewSimpleWaiter()}
	middlewares = append(middlewares, ratelimit.New(rate.Every(time.Millisecond*100), 5))
	return New(nil, storage, middlewares...), nil
}

func BotLogin(token string) (*telegram.Client, error) {
	config := utils.GetConfig()
	storage := kv.NewSession(database.KV, kv.Key("botsession", token))
	middlewares := []telegram.Middleware{floodwait.NewSimpleWaiter()}
	middlewares = append(middlewares, ratelimit.New(rate.Every(time.Millisecond*time.Duration(config.Rate)), config.RateBurst))
	return New(nil, storage, middlewares...), nil
}
