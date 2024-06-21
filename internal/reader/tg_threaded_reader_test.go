package reader

import (
	"context"
	"crypto/rand"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type testChunkSource struct {
	buffer []byte
}

func (m *testChunkSource) Chunk(ctx context.Context, offset int64, limit int64) ([]byte, error) {
	return m.buffer[offset : offset+limit], nil
}

func (m *testChunkSource) ChunkSize(start, end int64) int64 {
	return 1
}

type testChunkSourceTimeout struct {
	buffer []byte
}

func (m *testChunkSourceTimeout) Chunk(ctx context.Context, offset int64, limit int64) ([]byte, error) {
	if offset == 8 {
		time.Sleep(2 * time.Second)
	}
	return m.buffer[offset : offset+limit], nil
}

func (m *testChunkSourceTimeout) ChunkSize(start, end int64) int64 {
	return 1
}

type TestSuite struct {
	suite.Suite
}

func (suite *TestSuite) TestFullRead() {
	ctx := context.Background()
	start := int64(0)
	end := int64(99)
	concurrency := 8
	buffers := 10
	data := make([]byte, 100)
	rand.Read(data)
	chunkSrc := &testChunkSource{buffer: data}
	reader, err := newThreadedTGReader(ctx, start, end, concurrency, buffers, chunkSrc, time.Second)
	assert.NoError(suite.T(), err)
	test_data, err := io.ReadAll(reader)
	assert.Equal(suite.T(), nil, err)
	assert.Equal(suite.T(), data[start:end+1], test_data)
}

func (suite *TestSuite) TestPartialRead() {
	ctx := context.Background()
	start := int64(0)
	end := int64(65)
	concurrency := 8
	buffers := 10
	data := make([]byte, 100)
	rand.Read(data)
	chunkSrc := &testChunkSource{buffer: data}
	reader, err := newThreadedTGReader(ctx, start, end, concurrency, buffers, chunkSrc, time.Second)
	assert.NoError(suite.T(), err)
	test_data, err := io.ReadAll(reader)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), data[start:end+1], test_data)
}

func (suite *TestSuite) TestTimeout() {
	ctx := context.Background()
	start := int64(0)
	end := int64(65)
	concurrency := 8
	buffers := 10
	data := make([]byte, 100)
	rand.Read(data)
	chunkSrc := &testChunkSourceTimeout{buffer: data}
	reader, err := newThreadedTGReader(ctx, start, end, concurrency, buffers, chunkSrc, 1*time.Second)
	assert.NoError(suite.T(), err)
	test_data, err := io.ReadAll(reader)
	assert.Greater(suite.T(), len(test_data), 0)
	assert.Equal(suite.T(), err, ErrorStreamAbandoned)
}

func (suite *TestSuite) TestClose() {
	ctx := context.Background()
	start := int64(0)
	end := int64(65)
	concurrency := 8
	buffers := 10
	data := make([]byte, 100)
	rand.Read(data)
	chunkSrc := &testChunkSource{buffer: data}
	reader, err := newThreadedTGReader(ctx, start, end, concurrency, buffers, chunkSrc, time.Second)
	assert.NoError(suite.T(), err)
	_, err = io.ReadAll(reader)
	assert.NoError(suite.T(), err)
	assert.NoError(suite.T(), reader.Close())
}

func (suite *TestSuite) TestCancellation() {
	ctx, cancel := context.WithCancel(context.Background())
	start := int64(0)
	end := int64(65)
	concurrency := 8
	buffers := 10
	data := make([]byte, 100)
	rand.Read(data)
	chunkSrc := &testChunkSource{buffer: data}
	reader, err := newThreadedTGReader(ctx, start, end, concurrency, buffers, chunkSrc, time.Second)
	assert.NoError(suite.T(), err)
	cancel()
	_, err = io.ReadAll(reader)
	assert.Equal(suite.T(), err, context.Canceled)
	assert.Equal(suite.T(), len(reader.bufferChan), 0)
}

func (suite *TestSuite) TestCancellationWithTimeout() {
	ctx, _ := context.WithTimeout(context.Background(), 500*time.Millisecond)
	start := int64(0)
	end := int64(65)
	concurrency := 8
	buffers := 10
	data := make([]byte, 100)
	rand.Read(data)
	chunkSrc := &testChunkSourceTimeout{buffer: data}
	reader, err := newThreadedTGReader(ctx, start, end, concurrency, buffers, chunkSrc, time.Second)
	assert.NoError(suite.T(), err)
	_, err = io.ReadAll(reader)
	assert.Equal(suite.T(), err, context.DeadlineExceeded)
	assert.Equal(suite.T(), len(reader.bufferChan), 0)
}
func Test(t *testing.T) {
	suite.Run(t, new(TestSuite))
}
