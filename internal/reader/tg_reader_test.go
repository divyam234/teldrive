package reader

import (
	"context"
	"crypto/rand"
	"io"
	"testing"
	"time"

	"github.com/divyam234/teldrive/internal/config"
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
	config *config.TGConfig
}

func (suite *TestSuite) SetupTest() {
	suite.config = &config.TGConfig{Stream: struct {
		MultiThreads int
		Buffers      int
		ChunkTimeout time.Duration
	}{MultiThreads: 8, Buffers: 10, ChunkTimeout: 1 * time.Second}}
}

func (suite *TestSuite) TestFullRead() {
	ctx := context.Background()
	start := int64(0)
	end := int64(99)
	data := make([]byte, 100)
	rand.Read(data)
	chunkSrc := &testChunkSource{buffer: data}
	reader, err := newTGReader(ctx, start, end, suite.config, chunkSrc)
	assert.NoError(suite.T(), err)
	test_data, err := io.ReadAll(reader)
	assert.Equal(suite.T(), nil, err)
	assert.Equal(suite.T(), data[start:end+1], test_data)
}

func (suite *TestSuite) TestPartialRead() {
	ctx := context.Background()
	start := int64(0)
	end := int64(65)
	data := make([]byte, 100)
	rand.Read(data)
	chunkSrc := &testChunkSource{buffer: data}
	reader, err := newTGReader(ctx, start, end, suite.config, chunkSrc)
	assert.NoError(suite.T(), err)
	test_data, err := io.ReadAll(reader)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), data[start:end+1], test_data)
}

func (suite *TestSuite) TestTimeout() {
	ctx := context.Background()
	start := int64(0)
	end := int64(65)
	data := make([]byte, 100)
	rand.Read(data)
	chunkSrc := &testChunkSourceTimeout{buffer: data}
	reader, err := newTGReader(ctx, start, end, suite.config, chunkSrc)
	assert.NoError(suite.T(), err)
	test_data, err := io.ReadAll(reader)
	assert.Greater(suite.T(), len(test_data), 0)
	assert.Equal(suite.T(), err, ErrorStreamAbandoned)
}

func (suite *TestSuite) TestClose() {
	ctx := context.Background()
	start := int64(0)
	end := int64(65)
	data := make([]byte, 100)
	rand.Read(data)
	chunkSrc := &testChunkSource{buffer: data}
	reader, err := newTGReader(ctx, start, end, suite.config, chunkSrc)
	assert.NoError(suite.T(), err)
	_, err = io.ReadAll(reader)
	assert.NoError(suite.T(), err)
	assert.NoError(suite.T(), reader.Close())
}

func (suite *TestSuite) TestCancellation() {
	ctx, cancel := context.WithCancel(context.Background())
	start := int64(0)
	end := int64(65)
	data := make([]byte, 100)
	rand.Read(data)
	chunkSrc := &testChunkSource{buffer: data}
	reader, err := newTGReader(ctx, start, end, suite.config, chunkSrc)
	assert.NoError(suite.T(), err)
	cancel()
	_, err = io.ReadAll(reader)
	assert.Equal(suite.T(), err, context.Canceled)
	assert.Equal(suite.T(), len(reader.bufferChan), 0)
}

func (suite *TestSuite) TestCancellationWithTimeout() {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	_ = cancel
	start := int64(0)
	end := int64(65)
	data := make([]byte, 100)
	rand.Read(data)
	chunkSrc := &testChunkSourceTimeout{buffer: data}
	reader, err := newTGReader(ctx, start, end, suite.config, chunkSrc)
	assert.NoError(suite.T(), err)
	_, err = io.ReadAll(reader)
	assert.Equal(suite.T(), err, context.DeadlineExceeded)
	assert.Equal(suite.T(), len(reader.bufferChan), 0)
}
func Test(t *testing.T) {
	suite.Run(t, new(TestSuite))
}
