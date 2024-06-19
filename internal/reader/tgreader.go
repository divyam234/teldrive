package reader

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/gotd/td/tg"
	"golang.org/x/sync/errgroup"
)

type tgReader struct {
	ctx        context.Context
	client     *tg.Client
	location   *tg.InputDocumentFileLocation
	offset     int64
	limit      int64
	chunkSize  int64
	bufferChan chan *buffer
	cur        *buffer
	mu         sync.Mutex
	buffers    int
}

func calculateChunkSize(start, end int64) int64 {
	chunkSize := int64(1024 * 1024)

	for chunkSize > 1024 && chunkSize > (end-start) {
		chunkSize /= 2
	}

	return chunkSize
}

var bufferPoolOnce sync.Once
var bufferPool *sync.Pool

func initBufferPool() {
	bufferPool = &sync.Pool{
		New: func() interface{} {
			return &buffer{
				buf: make([]byte, 1024*1024),
			}
		},
	}
}

func newTGReader(
	ctx context.Context,
	client *tg.Client,
	location *tg.InputDocumentFileLocation,
	start int64,
	end int64,
	buffers int,

) (io.ReadCloser, error) {

	bufferPoolOnce.Do(initBufferPool)

	r := &tgReader{
		ctx:        ctx,
		location:   location,
		client:     client,
		offset:     start,
		chunkSize:  1024 * 1024,
		limit:      end - start + 1,
		bufferChan: make(chan *buffer, buffers*2),
		buffers:    buffers,
	}

	return r, nil
}

func (r *tgReader) Close() error {
	close(r.bufferChan)
	if r.cur != nil {
		bufferPool.Put(r.cur)
		r.cur = nil
	}
	return nil
}

func (r *tgReader) Read(p []byte) (n int, err error) {
	r.mu.Lock()

	defer r.mu.Unlock()

	if r.limit <= 0 {
		return -1, io.EOF
	}

	if r.cur == nil {
		go r.fillBuffer()
		r.cur = <-r.bufferChan
	} else if r.cur != nil && r.cur.isEmpty() {
		bufferPool.Put(r.cur)
		if len(r.bufferChan) == 0 {
			go r.fillBuffer()
		}
		r.cur = <-r.bufferChan
	}

	n = copy(p, r.cur.buffer())
	r.cur.increment(n)
	r.limit -= int64(n)
	return n, nil
}

func (r *tgReader) chunk(ctx context.Context, offset int64, limit int64) ([]byte, error) {

	req := &tg.UploadGetFileRequest{
		Offset:   offset,
		Limit:    int(limit),
		Location: r.location,
		Precise:  true,
	}

	res, err := r.client.UploadGetFile(ctx, req)

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

	offset := r.offset - (r.offset % r.chunkSize)

	g, ctx := errgroup.WithContext(r.ctx)

	threads := min(r.buffers, int(r.limit/r.chunkSize)+1)

	bufferMap := make(map[int]*buffer)

	for i := range threads {
		g.Go(func() error {
			chunk, err := r.chunk(ctx, offset+(int64(i)*r.chunkSize), r.chunkSize)
			if err != nil {
				return err
			}
			if i == 0 && r.cur == nil {
				chunk = chunk[r.offset-offset:]
			}
			buf := bufferPool.Get().(*buffer)
			buf.read(bytes.NewBuffer(chunk))
			bufferMap[i] = buf
			return nil
		})
	}
	start := time.Now()
	if err := g.Wait(); err != nil {
		close(r.bufferChan)
		return err
	}
	end := time.Now()
	duration := end.Sub(start)

	// Print the duration
	fmt.Printf("Time taken: %s\n", duration)

	for i := range threads {
		r.bufferChan <- bufferMap[i]
	}
	r.offset += r.chunkSize * int64(threads)
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
