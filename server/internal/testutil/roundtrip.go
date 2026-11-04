package testutil

import (
	"bytes"
	"testing"

	"github.com/infrapad/infrapad/server/pkg/content"
)

// RoundTripRecord encodes and decodes a record through the registry's host
// codecs, checking the encoding is stable, and returns the decoded record.
func RoundTripRecord[R content.Record](t testing.TB, registry *content.Registry, record R) content.Record {
	t.Helper()
	data, err := content.EncodeRecord(record)
	NoErr(t, err)
	decoded, err := registry.DecodeRecord(record.ContentType().Name(), data)
	NoErr(t, err)
	again, err := content.EncodeRecord(decoded)
	NoErr(t, err)
	if !bytes.Equal(data, again) {
		t.Fatalf("record changed through codec:\n%s\n%s", data, again)
	}
	return decoded
}
