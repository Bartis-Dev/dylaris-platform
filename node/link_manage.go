package main

// resolveNodeManagesLink decides whether this node runs its own Link sidecar.
//
// Setting LINK_IMAGE IS the opt-in: LINK_IMAGE has no other consumer, so a node
// that was handed an image is a node meant to manage its own Link. The explicit
// flag remains only as the escape hatch for the one topology where an image is
// configured but an operator deploys Link separately - without it, both would
// fight over the single fixed-name dylaris_link container.
//
// The previous default followed the node's external flag, which was wrong in a
// quiet way: a Link is needed per MC node whenever routing is gateway/both, not
// only for BYON - Core mints link credentials for every node at enroll. An
// in-cluster gateway node therefore came up with no Link and no error.
func resolveNodeManagesLink(envValue, linkImage string) bool {
	if envValue != "" {
		return envValue == "true"
	}
	return linkImage != ""
}
