package nodegrpc

import (
	"testing"

	pb "dylaris-proto/node"
)

func TestSendOnStreamUnknownNode(t *testing.T) {
	r := NewRegistry()
	if err := r.SendOnStream(999, &pb.NodeMessage{RequestId: "x"}); err == nil {
		t.Fatal("SendOnStream on an unconnected node = nil, want error")
	}
}
