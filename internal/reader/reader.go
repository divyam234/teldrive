package reader

import (
	"context"
	"io"

	"github.com/divyam234/teldrive/internal/config"
	"github.com/divyam234/teldrive/internal/tgc"
	"github.com/divyam234/teldrive/pkg/types"
)

func calculatePartByteRanges(startByte, endByte, partSize int64) []types.Range {

	partByteRanges := []types.Range{}

	startPart := startByte / partSize

	endPart := endByte / partSize

	startOffset := startByte % partSize

	for part := startPart; part <= endPart; part++ {
		partStartByte := int64(0)
		partEndByte := partSize - 1
		if part == startPart {
			partStartByte = startOffset
		}
		if part == endPart {
			partEndByte = int64(endByte % partSize)
		}
		partByteRanges = append(partByteRanges, types.Range{Start: partStartByte, End: partEndByte, PartNo: part})

		startOffset = 0
	}

	return partByteRanges
}

type linearReader struct {
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

func NewLinearReader(ctx context.Context,
	fileId string,
	parts []types.Part,
	start, end int64,
	channelId int64,
	config *config.TGConfig,
	concurrency int,
	client *tgc.Client,
	worker *tgc.StreamWorker,
) (reader io.ReadCloser, err error) {

	r := &linearReader{
		ctx:         ctx,
		parts:       parts,
		limit:       end - start + 1,
		ranges:      calculatePartByteRanges(start, end, parts[0].Size),
		config:      config,
		client:      client,
		worker:      worker,
		channelId:   channelId,
		fileId:      fileId,
		concurrency: concurrency,
	}

	r.reader, err = r.nextPart()

	if err != nil {
		return nil, err
	}

	return r, nil
}

func (r *linearReader) Read(p []byte) (int, error) {

	if r.limit <= 0 {
		return 0, io.EOF
	}

	n, err := r.reader.Read(p)

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
	return n, err
}

func (r *linearReader) nextPart() (io.ReadCloser, error) {

	start := r.ranges[r.pos].Start
	end := r.ranges[r.pos].End

	chunkSrc := &chunkSource{channelId: r.channelId, worker: r.worker,
		fileId: r.fileId, partId: r.parts[r.ranges[r.pos].PartNo].ID,
		client: r.client, concurrency: r.concurrency}
	return newTGReader(r.ctx, start, end, r.config, chunkSrc)

}

func (r *linearReader) Close() (err error) {
	if r.reader != nil {
		err = r.reader.Close()
		r.reader = nil
		return err
	}
	return nil
}
