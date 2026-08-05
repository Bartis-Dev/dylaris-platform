package handlers

import "testing"

// The upload handler parses the multipart body BEFORE the per-file, per-ticket
// and per-user quota checks can run, and ParseMultipartForm reads the whole
// request - spilling everything past its memory budget to a temp file. So
// without a body cap, a caller holding attach rights writes an arbitrarily large
// file to Core's disk and is told "too large" only afterwards.
//
// The server file upload already caps its body (file.go), and LimitBody's own
// comment assumed every upload handler did: "the upload handlers set their own,
// much larger MaxBytesReader". This one did not.
func TestTicketUploadBodyLimit(t *testing.T) {
	const mb = 1024 * 1024
	cases := []struct {
		name      string
		maxFileMB int
		want      int64
	}{
		{"the default per-file limit", 10, 10*mb + ticketUploadEnvelopeSlack},
		{"a raised limit", 200, 200*mb + ticketUploadEnvelopeSlack},
		{
			// "Unlimited" is about what an operator wants to ALLOW, not licence
			// to write unbounded data to disk before anything checks it.
			name:      "0 means unlimited, which still gets the hard cap",
			maxFileMB: 0, want: ticketUploadHardCapMB*mb + ticketUploadEnvelopeSlack,
		},
		{
			// LoadTicketSettings refuses to read a setting above 1024, but a value
			// that arrived another way must not raise the cap either.
			name:      "a value above the hard cap is clamped",
			maxFileMB: 99999, want: ticketUploadHardCapMB*mb + ticketUploadEnvelopeSlack,
		},
		{"a negative value is treated as unset", -5, ticketUploadHardCapMB*mb + ticketUploadEnvelopeSlack},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ticketUploadBodyLimit(c.maxFileMB); got != c.want {
				t.Errorf("ticketUploadBodyLimit(%d) = %d, want %d", c.maxFileMB, got, c.want)
			}
		})
	}
}

// A file of EXACTLY the per-file limit has to reach the quota check that is meant
// to judge it, so the cap must leave room for the multipart envelope. Without the
// slack the body cap would reject a legitimate at-the-limit upload with a 413
// before the handler ever compared it against the limit.
func TestTicketUploadBodyLimitLeavesRoomForTheEnvelope(t *testing.T) {
	const mb = 1024 * 1024
	for _, maxFileMB := range []int{1, 10, 200, 1024} {
		limit := ticketUploadBodyLimit(maxFileMB)
		fileBytes := int64(maxFileMB) * mb
		if limit <= fileBytes {
			t.Errorf("limit %d for a %d MB file leaves no room for the multipart envelope", limit, maxFileMB)
		}
	}
}
