package msggateway

import (
	"bytes"
	"compress/gzip"
	"io"
	"sync"

	"github.com/openimsdk/tools/errs"
)

var (
	gzipWriterPool = sync.Pool{New: func() any { return gzip.NewWriter(nil) }}
	gzipReaderPool = sync.Pool{New: func() any { return new(gzip.Reader) }}
)

type Compressor interface {
	Compress(rawData []byte) ([]byte, error)
	CompressWithPool(rawData []byte) ([]byte, error)
	DeCompress(compressedData []byte) ([]byte, error)
	DecompressWithPool(compressedData []byte) ([]byte, error)
}

type GzipCompressor struct {
	compressProtocol string
}

func NewGzipCompressor() *GzipCompressor {
	return &GzipCompressor{compressProtocol: "gzip"}
}

func (g *GzipCompressor) Compress(rawData []byte) ([]byte, error) {
	gzipBuffer := bytes.Buffer{}
	gz := gzip.NewWriter(&gzipBuffer)

	if _, err := gz.Write(rawData); err != nil {
		return nil, errs.WrapMsg(err, "GzipCompressor.Compress: writing to gzip writer failed")
	}

	if err := gz.Close(); err != nil {
		return nil, errs.WrapMsg(err, "GzipCompressor.Compress: closing gzip writer failed")
	}

	return gzipBuffer.Bytes(), nil
}

func (g *GzipCompressor) CompressWithPool(rawData []byte) ([]byte, error) {
	gz := gzipWriterPool.Get().(*gzip.Writer)
	defer gzipWriterPool.Put(gz)

	gzipBuffer := bytes.Buffer{}
	gz.Reset(&gzipBuffer)

	if _, err := gz.Write(rawData); err != nil {
		return nil, errs.WrapMsg(err, "GzipCompressor.CompressWithPool: error writing data")
	}
	if err := gz.Close(); err != nil {
		return nil, errs.WrapMsg(err, "GzipCompressor.CompressWithPool: error closing gzip writer")
	}
	return gzipBuffer.Bytes(), nil
}

func (g *GzipCompressor) DeCompress(compressedData []byte) ([]byte, error) {
	buff := bytes.NewBuffer(compressedData)
	reader, err := gzip.NewReader(buff)
	if err != nil {
		return nil, errs.WrapMsg(err, "GzipCompressor.DeCompress: NewReader creation failed")
	}
	decompressedData, err := io.ReadAll(reader)
	if err != nil {
		return nil, errs.WrapMsg(err, "GzipCompressor.DeCompress: reading from gzip reader failed")
	}
	if err = reader.Close(); err != nil {
		// 即使关闭 reader 失败，数据也已成功读出，
		// 因此返回解压后的数据，同时返回表示关闭失败的错误。
		return decompressedData, errs.WrapMsg(err, "GzipCompressor.DeCompress: closing gzip reader failed")
	}
	return decompressedData, nil
}

func (g *GzipCompressor) DecompressWithPool(compressedData []byte) ([]byte, error) {
	reader := gzipReaderPool.Get().(*gzip.Reader)
	defer gzipReaderPool.Put(reader)

	err := reader.Reset(bytes.NewReader(compressedData))
	if err != nil {
		return nil, errs.WrapMsg(err, "GzipCompressor.DecompressWithPool: resetting gzip reader failed")
	}

	decompressedData, err := io.ReadAll(reader)
	if err != nil {
		return nil, errs.WrapMsg(err, "GzipCompressor.DecompressWithPool: reading from pooled gzip reader failed")
	}
	if err = reader.Close(); err != nil {
		// 与 DeCompress 相同：返回数据以及关闭失败的错误。
		return decompressedData, errs.WrapMsg(err, "GzipCompressor.DecompressWithPool: closing pooled gzip reader failed")
	}
	return decompressedData, nil
}
