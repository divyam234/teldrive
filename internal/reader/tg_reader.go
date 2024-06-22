package reader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/divyam234/teldrive/internal/config"
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
	channelId   int64
	worker      *tgc.StreamWorker
	fileId      string
	partId      int64
	concurrency int
	client      *tgc.Client
}

func (c *chunkSource) ChunkSize(start, end int64) int64 {
	return tgc.CalculateChunkSize(start, end)
}

func (c *chunkSource) Chunk(ctx context.Context, offset int64, limit int64) ([]byte, error) {
	var (
		location *tg.InputDocumentFileLocation
		err      error
		client   *tgc.Client
	)

	client = c.client

	if c.concurrency > 0 {
		client, _, _ = c.worker.Next(c.channelId)
	}
	location, err = tgc.GetLocation(ctx, client, c.fileId, c.channelId, c.partId)

	if err != nil {
		return nil, err
	}

	return tgc.GetChunk(ctx, client.Tg.API(), location, offset, limit)

}

type tgReader struct {
	ctx         context.Context
	offset      int64
	limit       int64
	chunkSize   int64
	bufferChan  chan *buffer
	done        chan struct{}
	cur         *buffer
	err         chan error
	mu          sync.Mutex
	concurrency int
	leftCut     int64
	rightCut    int64
	totalParts  int
	currentPart int
	closed      bool
	timeout     time.Duration
	chunkSrc    ChunkSource
}

func newTGReader(
	ctx context.Context,
	start int64,
	end int64,
	config *config.TGConfig,
	chunkSrc ChunkSource,

) (*tgReader, error) {

	chunkSize := chunkSrc.ChunkSize(start, end)

	offset := start - (start % chunkSize)

	r := &tgReader{
		ctx:         ctx,
		limit:       end - start + 1,
		bufferChan:  make(chan *buffer, config.Stream.Buffers),
		concurrency: config.Stream.MultiThreads,
		leftCut:     start - offset,
		rightCut:    (end % chunkSize) + 1,
		totalParts:  int((end - offset + chunkSize) / chunkSize),
		offset:      offset,
		chunkSize:   chunkSize,
		chunkSrc:    chunkSrc,
		timeout:     config.Stream.ChunkTimeout,
		done:        make(chan struct{}, 1),
		err:         make(chan error, 1),
	}

	if r.concurrency == 0 {
		r.currentPart = 1
		go r.fillBufferSequentially()
	} else {
		go r.fillBufferConcurrently()
	}

	return r, nil
}

func (r *tgReader) Close() error {
	close(r.done)
	close(r.err)
	return nil
}

func (r *tgReader) Read(p []byte) (int, error) {
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
		case cur, ok := <-r.bufferChan:
			if !ok && r.limit > 0 {
				return 0, ErrorStreamAbandoned
			}
			r.cur = cur

		case err := <-r.err:
			return 0, fmt.Errorf("error reading chunk: %w", err)
		case <-r.ctx.Done():
			return 0, r.ctx.Err()

		}
	}

	n := copy(p, r.cur.buffer())
	r.cur.increment(n)
	r.limit -= int64(n)

	if r.limit <= 0 {
		return n, io.EOF
	}

	return n, nil
}

func (r *tgReader) fillBufferConcurrently() error {

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
				r.err <- err
				return nil
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

func (r *tgReader) fillBufferSequentially() error {

	defer close(r.bufferChan)

	fetchChunk := func(ctx context.Context) (*buffer, error) {
		chunk, err := r.chunkSrc.Chunk(ctx, r.offset, r.chunkSize)
		if err != nil {
			return nil, err
		}
		if r.totalParts == 1 {
			chunk = chunk[r.leftCut:r.rightCut]
		} else if r.currentPart == 1 {
			chunk = chunk[r.leftCut:]
		} else if r.currentPart == r.totalParts {
			chunk = chunk[:r.rightCut]
		}
		return &buffer{buf: chunk}, nil
	}

	for {
		select {
		case <-r.done:
			return nil
		case <-r.ctx.Done():
			return r.ctx.Err()
		case <-time.After(r.timeout):
			return nil
		default:
			buf, err := fetchChunk(r.ctx)
			if err != nil {
				r.err <- err
				return nil
			}
			r.bufferChan <- buf
			r.currentPart++
			r.offset += r.chunkSize
			if r.currentPart > r.totalParts {
				return nil
			}
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
