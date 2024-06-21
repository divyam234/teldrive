package reader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/divyam234/teldrive/internal/cache"
	"github.com/divyam234/teldrive/internal/tgc"
	"github.com/gotd/td/tg"
	"golang.org/x/sync/errgroup"
)

var ErrorStreamAbandoned = errors.New("stream abandoned")

type ChunkSource interface {
	Chunk(ctx context.Context, offset int64, limit int64) ([]byte, error)
	ChunkSize(start, end int64) int64
}

type chunkSource struct {
	channelId int64
	worker    *tgc.StreamWorker
	fileId    string
	partId    int64
}

func (c *chunkSource) ChunkSize(start, end int64) int64 {
	return calculateChunkSize(start, end)
}

func (c *chunkSource) Chunk(ctx context.Context, offset int64, limit int64) ([]byte, error) {

	cache := cache.FromContext(ctx)

	var location *tg.InputDocumentFileLocation

	client, _, _ := c.worker.Next(c.channelId)

	key := fmt.Sprintf("location:%s:%s:%d", client.UserId, c.fileId, c.partId)

	err := cache.Get(key, location)

	if err != nil {
		channel, err := tgc.GetChannelById(ctx, client.Tg.API(), c.channelId)

		if err != nil {
			return nil, err
		}
		messageRequest := tg.ChannelsGetMessagesRequest{
			Channel: channel,
			ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: int(c.partId)}},
		}

		res, err := client.Tg.API().ChannelsGetMessages(ctx, &messageRequest)
		if err != nil {
			return nil, err
		}
		messages, _ := res.(*tg.MessagesChannelMessages)
		item := messages.Messages[0].(*tg.Message)
		media := item.Media.(*tg.MessageMediaDocument)
		document := media.Document.(*tg.Document)
		location = document.AsInputDocumentFileLocation()
		cache.Set(key, location, 3600)
	}

	req := &tg.UploadGetFileRequest{
		Offset:   offset,
		Limit:    int(limit),
		Location: location,
		Precise:  true,
	}

	res, err := client.Tg.API().UploadGetFile(ctx, req)

	if err != nil {
		return nil, err
	}

	switch result := res.(type) {
	case *tg.UploadFile:
		return result.Bytes, nil
	default:
		return nil, fmt.Errorf("unexpected type %T", c)
	}
}

type threadedTgReader struct {
	ctx         context.Context
	offset      int64
	limit       int64
	chunkSize   int64
	bufferChan  chan *buffer
	done        chan struct{}
	cur         *buffer
	mu          sync.Mutex
	concurrency int
	leftCut     int64
	rightCut    int64
	totalParts  int
	currentPart int
	closed      bool
	chunkSrc    ChunkSource
	timeout     time.Duration
}

func newThreadedTGReader(
	ctx context.Context,
	start int64,
	end int64,
	concurrency int,
	buffers int,
	chunkSrc ChunkSource,
	timeout time.Duration,

) (*threadedTgReader, error) {

	chunkSize := chunkSrc.ChunkSize(start, end)

	offset := start - (start % chunkSize)

	r := &threadedTgReader{
		ctx:         ctx,
		limit:       end - start + 1,
		bufferChan:  make(chan *buffer, buffers),
		concurrency: concurrency,
		leftCut:     start - offset,
		rightCut:    (end % chunkSize) + 1,
		totalParts:  int((end - offset + chunkSize) / chunkSize),
		offset:      offset,
		chunkSize:   chunkSize,
		chunkSrc:    chunkSrc,
		timeout:     timeout,
		done:        make(chan struct{}, 1),
	}

	go r.fillBuffer()

	return r, nil
}

func (r *threadedTgReader) Close() error {
	close(r.done)
	if !r.closed {
		close(r.bufferChan)
	}
	return nil
}

func (r *threadedTgReader) Read(p []byte) (n int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cur.isEmpty() {
		if r.cur != nil {
			r.cur = nil
		}
		select {
		case cur, ok := <-r.bufferChan:
			if !ok {
				return 0, ErrorStreamAbandoned
			}
			r.cur = cur
		case <-r.ctx.Done():
			return 0, r.ctx.Err()

		}
	}

	n = copy(p, r.cur.buffer())
	r.cur.increment(n)
	r.limit -= int64(n)

	if r.limit <= 0 {
		return n, io.EOF
	}

	return n, nil
}

func (r *threadedTgReader) fillBuffer() error {

	var mapMu sync.Mutex

	bufferMap := make(map[int]*buffer)

	defer func() {
		close(r.bufferChan)
		r.closed = true
		for i := range bufferMap {
			delete(bufferMap, i)
		}
	}()

	cb := func(ctx context.Context, i int) func() error {
		return func() error {

			chunk, err := r.chunkSrc.Chunk(ctx, r.offset+(int64(i)*r.chunkSize), r.chunkSize)
			if err != nil {
				return err
			}
			if r.totalParts == 1 {
				chunk = chunk[r.leftCut:r.rightCut]
			} else if r.currentPart+i+1 == 1 {
				chunk = chunk[r.leftCut:]
			} else if r.currentPart+i+1 == r.totalParts {
				chunk = chunk[:r.rightCut]
			}
			buf := &buffer{buf: chunk}
			mapMu.Lock()
			bufferMap[i] = buf
			mapMu.Unlock()
			return nil
		}
	}

	for {

		g := errgroup.Group{}

		g.SetLimit(r.concurrency)

		for i := range r.concurrency {
			if r.currentPart+i+1 <= r.totalParts {
				g.Go(cb(r.ctx, i))
			}
		}

		done := make(chan error, 1)

		go func() {
			done <- g.Wait()
		}()

		select {
		case err := <-done:
			if err != nil {
				return err
			} else {
				for i := range r.concurrency {
					if r.currentPart+i+1 <= r.totalParts {
						r.bufferChan <- bufferMap[i]
					}
				}
				r.currentPart += r.concurrency
				r.offset += r.chunkSize * int64(r.concurrency)
				for i := range bufferMap {
					delete(bufferMap, i)
				}
				if r.currentPart >= r.totalParts {
					return nil
				}
			}
		case <-time.After(r.timeout):
			return nil
		case <-r.done:
			return nil
		case <-r.ctx.Done():
			return r.ctx.Err()
		}

	}
}

type buffer struct {
	buf    []byte
	offset int
}

func (b *buffer) isEmpty() bool {
	if b == nil {
		return true
	}
	if len(b.buf)-b.offset <= 0 {
		return true
	}
	return false
}

func (b *buffer) buffer() []byte {
	return b.buf[b.offset:]
}

func (b *buffer) increment(n int) {
	b.offset += n
}
