package reader

import (
	"context"
	"io"

	"github.com/divyam234/teldrive/internal/config"
	"github.com/divyam234/teldrive/internal/crypt"
	"github.com/divyam234/teldrive/internal/tgc"
	"github.com/divyam234/teldrive/pkg/types"
)

type decrpytedReader struct {
	ctx         context.Context
	parts       []types.Part
	ranges      []types.Range
	pos         int
	reader      io.ReadCloser
	limit       int64
	config      *config.TGConfig
	channelId   int64
	worker      *tgc.StreamWorker
	client      *tgc.Client
	fileId      string
	concurrency int
}

func NewDecryptedReader(
	ctx context.Context,
	fileId string,
	parts []types.Part,
	start, end int64,
	channelId int64,
	config *config.TGConfig,
	concurrency int,
	client *tgc.Client,
	worker *tgc.StreamWorker) (io.ReadCloser, error) {

	r := &decrpytedReader{
		ctx:         ctx,
		parts:       parts,
		limit:       end - start + 1,
		ranges:      calculatePartByteRanges(start, end, parts[0].DecryptedSize),
		config:      config,
		client:      client,
		worker:      worker,
		channelId:   channelId,
		fileId:      fileId,
		concurrency: concurrency,
	}
	res, err := r.nextPart()

	if err != nil {
		return nil, err
	}

	r.reader = res

	return r, nil

}

func (r *decrpytedReader) Read(p []byte) (n int, err error) {

	if r.limit <= 0 {
		return 0, io.EOF
	}

	n, err = r.reader.Read(p)
	r.limit -= int64(n)
	if err == io.EOF {
		if r.limit > 0 {
			err = nil
			if r.reader != nil {
				r.reader.Close()
			}
		}
		r.pos++
		if r.pos < len(r.ranges) {
			r.reader, err = r.nextPart()
		}
	}
	return
}

func (r *decrpytedReader) Close() (err error) {
	if r.reader != nil {
		err = r.reader.Close()
		r.reader = nil
		return err
	}
	return nil
}

func (r *decrpytedReader) nextPart() (io.ReadCloser, error) {

	start := r.ranges[r.pos].Start
	end := r.ranges[r.pos].End
	salt := r.parts[r.ranges[r.pos].PartNo].Salt
	cipher, _ := crypt.NewCipher(r.config.Uploads.EncryptionKey, salt)

	return cipher.DecryptDataSeek(r.ctx,
		func(ctx context.Context,
			underlyingOffset,
			underlyingLimit int64) (io.ReadCloser, error) {
			var end int64

			if underlyingLimit >= 0 {
				end = min(r.parts[r.ranges[r.pos].PartNo].Size-1, underlyingOffset+underlyingLimit-1)
			}
			chunkSrc := &chunkSource{channelId: r.channelId, worker: r.worker,
				fileId: r.fileId, partId: r.parts[r.ranges[r.pos].PartNo].ID,
				client: r.client, concurrency: r.concurrency}
			return newTGReader(r.ctx, start, end, r.config, chunkSrc)

		}, start, end-start+1)

}
