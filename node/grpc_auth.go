package main

import (
	"fmt"

	pb "dylaris-proto/node"
)

// recvAuthResult reads from the auth stream until the Core delivers an
// AuthResult, transparently answering a challenge nonce along the way. On the
// hasSecret reconnect path an upgraded Core sends a NodeChallenge before the
// AuthResult; the node replies with HMAC(secret, nonce) so a captured proof
// cannot be replayed. secret is nil on paths that never receive a challenge
// (ACL off, or first enrollment); a challenge arriving with no secret is a hard
// error (a node that lost its secret must recover via operator action, not
// silently re-auth).
func recvAuthResult(stream pb.NodeService_NodeConnectClient, secret []byte) (*pb.AuthResult, error) {
	for {
		msg, err := stream.Recv()
		if err != nil {
			return nil, err
		}
		if ch := msg.GetChallenge(); ch != nil {
			if secret == nil {
				return nil, fmt.Errorf("core sent a challenge but node has no secret")
			}
			resp := aclChallengeResponse(secret, ch.Nonce)
			if serr := stream.Send(&pb.NodeMessage{
				Payload: &pb.NodeMessage_ChallengeResponse{
					ChallengeResponse: &pb.NodeChallengeResponse{Response: resp},
				},
			}); serr != nil {
				return nil, fmt.Errorf("send challenge response: %w", serr)
			}
			continue
		}
		if ar := msg.GetAuthResult(); ar != nil {
			return ar, nil
		}
		return nil, fmt.Errorf("unexpected message during auth (want challenge or auth_result)")
	}
}
