package ftpserver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPortRangeEdgeCases tests edge cases for PortRange
func TestPortRangeEdgeCases(t *testing.T) {
	req := require.New(t)

	// Test with single port range
	portRange := PortRange{
		Start: 8080,
		End:   8080,
	}

	exposedPort, listenedPort, ok := portRange.FetchNext()
	req.True(ok)
	req.Equal(8080, exposedPort)
	req.Equal(8080, listenedPort)
	req.Equal(1, portRange.NumberAttempts())
}

// TestPortMappingRangeEdgeCases tests edge cases for PortMappingRange
func TestPortMappingRangeEdgeCases(t *testing.T) {
	req := require.New(t)

	// Test with single port mapping
	portMappingRange := PortMappingRange{
		ExposedStart:  8000,
		ListenedStart: 9000,
		Count:         1,
	}

	exposedPort, listenedPort, ok := portMappingRange.FetchNext()
	req.True(ok)
	req.Equal(8000, exposedPort)
	req.Equal(9000, listenedPort)
	req.Equal(1, portMappingRange.NumberAttempts())
}

// TestAdditionalErrorCases tests additional error cases
func TestAdditionalErrorCases(t *testing.T) {
	req := require.New(t)

	// Test ErrStorageExceeded
	req.Equal("storage exceeded", ErrStorageExceeded.Error())

	// Test ErrFileNameNotAllowed
	req.Equal("file name not allowed", ErrFileNameNotAllowed.Error())

	// Test ErrNoAvailableListeningPort
	req.Equal("no available listening port", ErrNoAvailableListeningPort.Error())
}
