package reader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/divyam234/teldrive/internal/cache"
	"github.com/divyam234/teldrive/internal/tgc"
	"github.com/gotd/td/tg"
	"golang.org/x/sync/errgroup"
)

var ErrorStreamAbandoned = errors.New("stream abandoned")

type threadedTgReader struct {
	ctx         context.Context
	offset      int64
	limit       int64
	chunkSize   int64
	bufferChan  chan *buffer
	cur         *buffer
	mu          sync.Mutex
	concurrency int
	done        chan struct{}
	wg          sync.WaitGroup
	leftCut     int64
	rightCut    int64
	totalParts  int
	currentPart int
	channelId   int64
	worker      *tgc.StreamWorker
	fileId      string
	partId      int64
}

func newThreadedTGReader(
	ctx context.Context,
	fileID string,
	partId int64,
	start int64,
	end int64,
	concurrency int,
	buffers int, channelId int64,
	worker *tgc.StreamWorker,

) (io.ReadCloser, error) {

	chunkSize := calculateChunkSize(start, end)

	offset := start - (start % chunkSize)

	r := &threadedTgReader{
		ctx:         ctx,
		limit:       end - start + 1,
		bufferChan:  make(chan *buffer, buffers),
		concurrency: concurrency,
		done:        make(chan struct{}),
		leftCut:     start - offset,
		rightCut:    (end % chunkSize) + 1,
		totalParts:  int((end - offset + chunkSize) / chunkSize),
		offset:      offset,
		chunkSize:   chunkSize,
		channelId:   channelId,
		worker:      worker,
		fileId:      fileID,
		partId:      partId,
	}

	r.wg.Add(1)

	go r.fillBuffer()

	return r, nil
}

func (r *threadedTgReader) Close() error {
	close(r.done)
	r.wg.Wait()
	close(r.bufferChan)
	return nil
}

func (r *threadedTgReader) Read(p []byte) (n int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.limit <= 0 {
		return 0, io.EOF
	}

	if r.cur.isEmpty() {
		if r.cur != nil {
			r.cur = nil
		}
		select {
		case <-r.done:
			return 0, ErrorStreamAbandoned
		case cur := <-r.bufferChan:
			r.cur = cur
		case <-r.ctx.Done():
			return 0, r.ctx.Err()
		}
	}

	n = copy(p, r.cur.buffer())
	r.cur.increment(n)
	r.limit -= int64(n)

	return n, nil
}

func (r *threadedTgReader) chunk(ctx context.Context, offset int64, limit int64) ([]byte, error) {

	cache := cache.FromContext(ctx)

	var location *tg.InputDocumentFileLocation

	client, _, _ := r.worker.Next(r.channelId)

	key := fmt.Sprintf("location:%s:%s:%d", client.UserId, r.fileId, r.partId)

	err := cache.Get(key, location)

	if err != nil {
		channel, err := tgc.GetChannelById(ctx, client.Tg.API(), r.channelId)

		if err != nil {
			return nil, err
		}
		messageRequest := tg.ChannelsGetMessagesRequest{
			Channel: channel,
			ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: int(r.partId)}},
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
		return nil, fmt.Errorf("unexpected type %T", r)
	}
}

func (r *threadedTgReader) fillBuffer() error {

	var mapMu sync.Mutex

	bufferMap := make(map[int]*buffer)

	defer func() {
		r.wg.Done()
		for i := range bufferMap {
			delete(bufferMap, i)
		}
	}()

	cb := func(ctx context.Context, i int) func() error {
		return func() error {

			chunk, err := r.chunk(ctx, r.offset+(int64(i)*r.chunkSize), r.chunkSize)
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

loop:
	for {
		select {
		case <-r.done:
			break loop
		case <-r.ctx.Done():
			break loop
		default:
			g, ctx := errgroup.WithContext(r.ctx)

			g.SetLimit(r.concurrency)

			for i := range r.concurrency {
				if r.currentPart+i+1 <= r.totalParts {
					g.Go(cb(ctx, i))
				}
			}

			if err := g.Wait(); err != nil {
				return err
			}
			for i := range r.concurrency {
				if r.currentPart+i+1 <= r.totalParts {
					select {
					case <-r.done:
						break loop
					case <-r.ctx.Done():
						break loop
					case r.bufferChan <- bufferMap[i]:
					}
				}
			}
			r.currentPart += r.concurrency
			r.offset += r.chunkSize * int64(r.concurrency)
			for i := range bufferMap {
				delete(bufferMap, i)
			}
			if r.currentPart >= r.totalParts {
				break loop
			}
		}
	}
	return nil
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
