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
	ctx       context.Context
	parts     []types.Part
	ranges    []types.Range
	pos       int
	reader    io.ReadCloser
	limit     int64
	err       error
	config    *config.TGConfig
	channelId int64
	worker    *tgc.StreamWorker
	fileId    string
}

func NewLinearReader(ctx context.Context,
	fileId string,
	parts []types.Part,
	start, end int64,
	channelId int64,
	config *config.TGConfig,
	worker *tgc.StreamWorker,
) (reader io.ReadCloser, err error) {

	r := &linearReader{
		ctx:       ctx,
		parts:     parts,
		limit:     end - start + 1,
		ranges:    calculatePartByteRanges(start, end, parts[0].Size),
		config:    config,
		worker:    worker,
		channelId: channelId,
		fileId:    fileId,
	}

	r.reader, err = r.nextPart()

	if err != nil {
		return nil, err
	}

	return r, nil
}

func (r *linearReader) Read(p []byte) (n int, err error) {

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
			if r.reader != nil {
				r.reader.Close()
			}
		}
		r.pos++
		if r.pos < len(r.ranges) {
			r.reader, err = r.nextPart()

		}
	}
	r.err = err
	return
}

func (r *linearReader) nextPart() (io.ReadCloser, error) {

	startByte := r.ranges[r.pos].Start
	endByte := r.ranges[r.pos].End
	return newTGReader(r.ctx, r.fileId, r.parts[r.ranges[r.pos].PartNo].ID,
		startByte, endByte, r.config.BgBotsLimit, r.channelId, r.worker)

}

func (r *linearReader) Close() (err error) {
	if r.reader != nil {
		err = r.reader.Close()
		r.reader = nil
		return err
	}
	return nil
}
