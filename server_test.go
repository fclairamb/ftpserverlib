package ftpserver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPortCommandFormatOK(t *testing.T) {
	net, err := parsePORTAddr("127,0,0,1,239,163")
	require.NoError(t, err, "Problem parsing")
	require.Equal(t, "127.0.0.1", net.IP.String(), "Problem parsing IP")
	require.Equal(t, 239<<8+163, net.Port, "Problem parsing port")
}

func TestPortCommandFormatInvalid(t *testing.T) {
	badFormats := []string{
		"127,0,0,1,239,",
		"127,0,0,1,1,1,1",
	}
	for _, f := range badFormats {
		_, err := parsePORTAddr(f)
		require.Error(t, err, "This should have failed")
	}
}

func TestQuoteDoubling(t *testing.T) {
	type args struct {
		s string
	}

	tests := []struct {
		name string
		args args
		want string
	}{
		// TODO: Add test cases.
		{"1", args{" white space"}, " white space"},
		{"1", args{` one" quote`}, ` one"" quote`},
		{"1", args{` two"" quote`}, ` two"""" quote`},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, quoteDoubling(tt.args.s))
		})
	}
}
