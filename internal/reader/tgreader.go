package reader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/divyam234/teldrive/internal/cache"
	"github.com/divyam234/teldrive/internal/tgc"
	"github.com/gotd/td/tg"
	"github.com/rclone/rclone/lib/pool"
	"golang.org/x/sync/errgroup"
)

const (
	BufferSize           = 1024 * 1024
	bufferCacheSize      = 64
	bufferCacheFlushTime = 5 * time.Second
)

var ErrorStreamAbandoned = errors.New("stream abandoned")

type tgReader struct {
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

func calculateChunkSize(start, end int64) int64 {
	chunkSize := int64(1024 * 1024)

	for chunkSize > 1024 && chunkSize > (end-start) {
		chunkSize /= 2
	}

	return chunkSize
}

func newTGReader(
	ctx context.Context,
	fileID string,
	partId int64,
	start int64,
	end int64,
	concurrency int,
	channelId int64,
	worker *tgc.StreamWorker,

) (io.ReadCloser, error) {

	chunkSize := calculateChunkSize(start, end)

	offset := start - (start % chunkSize)

	r := &tgReader{
		ctx:         ctx,
		limit:       end - start + 1,
		bufferChan:  make(chan *buffer, 64),
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

var bufferPool *pool.Pool

var bufferPoolOnce sync.Once

func (r *tgReader) putBuffer(b *buffer) {
	bufferPool.Put(b.buf)
	b.buf = nil
}

func (r *tgReader) getBuffer() *buffer {
	bufferPoolOnce.Do(func() {
		bufferPool = pool.New(bufferCacheFlushTime, BufferSize, bufferCacheSize, false)
	})
	return &buffer{
		buf: bufferPool.Get(),
	}
}

func (r *tgReader) Close() error {
	close(r.done)
	r.wg.Wait()
	close(r.bufferChan)

	if r.cur != nil {
		r.putBuffer(r.cur)
		r.cur = nil
	}
	for b := range r.bufferChan {
		r.putBuffer(b)
	}
	bufferPool.Flush()
	return nil
}

func (r *tgReader) Read(p []byte) (n int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.limit <= 0 {
		return 0, io.EOF
	}

	if r.cur.isEmpty() {
		if r.cur != nil {
			r.putBuffer(r.cur)
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

func (r *tgReader) chunk(ctx context.Context, offset int64, limit int64) ([]byte, error) {

	cache := cache.FromContext(ctx)

	var location *tg.InputDocumentFileLocation

	client, _, _ := r.worker.Next(r.channelId)

	key := fmt.Sprintf("location:%s:%s:%d", client.UserId, r.fileId, r.partId)

	err := cache.Get(key, location)

	if err != nil {
		channel := &tg.InputChannel{}
		inputChannel := &tg.InputChannel{
			ChannelID: r.channelId,
		}
		channels, err := client.Tg.API().ChannelsGetChannels(ctx, []tg.InputChannelClass{inputChannel})

		if err != nil {
			return nil, err
		}

		channel = channels.GetChats()[0].(*tg.Channel).AsInput()
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

func (r *tgReader) fillBuffer() error {
	defer r.wg.Done()
	var mapMu sync.Mutex
loop:
	for {
		select {
		case <-r.done:
			break loop
		case <-r.ctx.Done():
			break loop
		default:

			g, ctx := errgroup.WithContext(r.ctx)

			threads := min(r.concurrency, int(r.limit/r.chunkSize)+1)

			bufferMap := make(map[int]*buffer)

			for i := range threads {
				g.Go(func() error {
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

					mapMu.Lock()
					buf := r.getBuffer()
					buf.read(bytes.NewReader(chunk))
					bufferMap[i] = buf
					mapMu.Unlock()
					return nil
				})
			}
			if err := g.Wait(); err != nil {
				return err
			}

			for i := range threads {
				select {
				case <-r.done:
					break loop
				case <-r.ctx.Done():
					break loop
				case r.bufferChan <- bufferMap[i]:
				}
			}
			r.currentPart += threads
			r.offset += r.chunkSize * int64(threads)
		}
	}
	return nil
}

type buffer struct {
	buf    []byte
	err    error
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

func (b *buffer) readFill(r io.Reader, buf []byte) (n int, err error) {
	var nn int
	for n < len(buf) && err == nil {
		nn, err = r.Read(buf[n:])
		n += nn
	}
	return n, err
}

func (b *buffer) read(rd io.Reader) error {
	var n int
	n, b.err = b.readFill(rd, b.buf)
	b.buf = b.buf[0:n]
	b.offset = 0
	return b.err
}

func (b *buffer) buffer() []byte {
	return b.buf[b.offset:]
}

func (b *buffer) increment(n int) {
	b.offset += n
}
