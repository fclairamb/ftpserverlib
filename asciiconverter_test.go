package ftpserver

import (
	"bytes"
	"io"
	"io/ioutil"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestASCIIConvert(t *testing.T) {
	lines := []byte("line1\r\nline2\r\n\r\nline4")
	src := bytes.NewBuffer(lines)
	dst := bytes.NewBuffer(nil)
	c := newASCIIConverter(src, convertModeToLF)
	_, err := io.Copy(dst, c)
	require.NoError(t, err)
	require.Equal(t, []byte("line1\nline2\n\nline4"), dst.Bytes())

	lines = []byte("line1\nline2\n\nline4")
	dst = bytes.NewBuffer(nil)
	c = newASCIIConverter(bytes.NewBuffer(lines), convertModeToCRLF)
	_, err = io.Copy(dst, c)
	require.NoError(t, err)
	require.Equal(t, []byte("line1\r\nline2\r\n\r\nline4"), dst.Bytes())

	// test a src buffers without line endings, it must remain unchanged
	buf := make([]byte, 131072)
	for j := range buf {
		buf[j] = 66
	}

	dst = bytes.NewBuffer(nil)
	c = newASCIIConverter(bytes.NewBuffer(buf), convertModeToCRLF)
	_, err = io.Copy(dst, c)
	require.NoError(t, err)
	require.Equal(t, buf, dst.Bytes())
}

func BenchmarkASCIIConverter(b *testing.B) {
	linesCRLF := []byte("line1\r\nline2\r\n\r\nline4")
	linesLF := []byte("line1\nline2\n\nline4")

	readerCRLF := bytes.NewBuffer(nil)
	readerLF := bytes.NewBuffer(nil)

	for i := 0; i < 100000; i++ {
		_, err := readerCRLF.Write(linesCRLF)
		panicOnError(err)

		_, err = readerLF.Write(linesLF)
		panicOnError(err)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		c := newASCIIConverter(readerCRLF, convertModeToLF)
		_, err := io.Copy(ioutil.Discard, c)
		panicOnError(err)

		c = newASCIIConverter(readerLF, convertModeToCRLF)
		_, err = io.Copy(ioutil.Discard, c)
		panicOnError(err)
	}
}
