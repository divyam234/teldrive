package tgc

import (
	"context"
	"strings"
	"sync"

	"github.com/divyam234/teldrive/internal/config"
	"github.com/divyam234/teldrive/internal/kv"
	"github.com/gotd/td/telegram"
)

type UploadWorker struct {
	mu      sync.Mutex
	bots    map[int64][]string
	currIdx map[int64]int
}

func (w *UploadWorker) Set(bots []string, channelId int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.bots[channelId]
	if !ok {
		w.bots = make(map[int64][]string)
		w.currIdx = make(map[int64]int)
		w.bots[channelId] = bots
		w.currIdx[channelId] = 0
	}
}

func (w *UploadWorker) Next(channelId int64) (string, int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	index := w.currIdx[channelId]
	w.currIdx[channelId] = (index + 1) % len(w.bots[channelId])
	return w.bots[channelId][index], index
}

func NewUploadWorker() *UploadWorker {
	return &UploadWorker{}
}

type Client struct {
	Tg     *telegram.Client
	Stop   StopFunc
	Status string
	UserId string
}

type StreamWorker struct {
	mu      sync.Mutex
	bots    map[int64][]string
	clients map[int64][]*Client
	currIdx map[int64]int
	cnf     *config.TGConfig
	kv      kv.KV
	ctx     context.Context
}

func (w *StreamWorker) Set(bots []string, channelId int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.bots[channelId]
	if !ok {
		w.bots = make(map[int64][]string)
		w.clients = make(map[int64][]*Client)
		w.currIdx = make(map[int64]int)
		w.bots[channelId] = bots
		for _, token := range bots {
			middlewares := Middlewares(w.cnf, 5)
			client, _ := BotClient(w.ctx, w.kv, w.cnf, token, middlewares...)
			c := &Client{Tg: client, Status: "idle", UserId: strings.Split(token, ":")[0]}
			w.clients[channelId] = append(w.clients[channelId], c)
		}
		w.currIdx[channelId] = 0
	}

}

func (w *StreamWorker) Next(channelId int64) (*Client, int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	index := w.currIdx[channelId]
	nextClient := w.clients[channelId][index]
	w.currIdx[channelId] = (index + 1) % len(w.clients[channelId])
	if nextClient.Status == "idle" {
		stop, err := Connect(nextClient.Tg, WithBotToken(w.bots[channelId][index]))
		if err != nil {
			return nil, 0, err
		}
		nextClient.Stop = stop
		nextClient.Status = "running"
	}
	return nextClient, index, nil
}

func (w *StreamWorker) UserWorker(session string, userId int64) (*Client, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	_, ok := w.clients[userId]

	if !ok {
		w.clients = make(map[int64][]*Client)
		middlewares := Middlewares(w.cnf, 5)
		client, _ := AuthClient(w.ctx, w.cnf, session, middlewares...)
		c := &Client{Tg: client, Status: "idle"}
		w.clients[userId] = append(w.clients[userId], c)
	}
	nextClient := w.clients[userId][0]
	if nextClient.Status == "idle" {
		stop, err := Connect(nextClient.Tg, WithContext(w.ctx))
		if err != nil {
			return nil, err
		}
		nextClient.Stop = stop
		nextClient.Status = "running"
	}
	return nextClient, nil
}

func NewStreamWorker(ctx context.Context) func(cnf *config.Config, kv kv.KV) *StreamWorker {
	return func(cnf *config.Config, kv kv.KV) *StreamWorker {
		return &StreamWorker{cnf: &cnf.TG, kv: kv, ctx: ctx}
	}

}
