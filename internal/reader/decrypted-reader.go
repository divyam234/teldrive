package reader

import (
	"context"
	"io"

	"github.com/divyam234/teldrive/internal/config"
	"github.com/divyam234/teldrive/internal/crypt"
	"github.com/divyam234/teldrive/pkg/types"
	"github.com/gotd/td/tg"
)

type decrpytedReader struct {
	ctx    context.Context
	parts  []types.Part
	ranges []types.Range
	pos    int
	client *tg.Client
	reader io.ReadCloser
	limit  int64
	err    error
	config *config.TGConfig
}

func NewDecryptedReader(
	ctx context.Context,
	client *tg.Client,
	parts []types.Part,
	start, end int64,
	config *config.TGConfig) (io.ReadCloser, error) {

	r := &decrpytedReader{
		ctx:    ctx,
		parts:  parts,
		client: client,
		limit:  end - start + 1,
		ranges: calculatePartByteRanges(start, end, parts[0].DecryptedSize),
		config: config,
	}
	res, err := r.nextPart()

	if err != nil {
		return nil, err
	}

	r.reader = res

	return r, nil

}

func (r *decrpytedReader) Read(p []byte) (n int, err error) {

	if r.err != nil {
		return 0, r.err
	}

	if r.limit <= 0 {
		return 0, io.EOF
	}

	n, err = r.reader.Read(p)

	if err == nil {
		r.limit -= int64(n)
	}

	if err == io.EOF {
		if r.limit > 0 {
			err = nil
		}
		r.pos++
		if r.pos < len(r.ranges) {
			r.reader, err = r.nextPart()
		}
	}
	r.err = err
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

	location := r.parts[r.ranges[r.pos].PartNo].Location
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
			return newTGReader(r.ctx, r.client, location, underlyingOffset, end, 16)

		}, start, end-start+1)

}
