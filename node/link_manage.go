package main

import "strings"

// defaultLinkImage is the Link the node spawns when LINK_IMAGE is not set.
//
// It is a default for the IMAGE only and deliberately not an opt-in for
// managing one: resolveNodeManagesLink is asked about the raw env value, so a
// default here cannot make an in-cluster node start spawning a Link it was
// never meant to run.
const defaultLinkImage = "ghcr.io/bartis-dev/dylaris-gateway-link:latest"

// resolveLinkImage picks the Link image, falling back to the built-in default.
// The BYON deploy snippet used to have to carry the image string purely because
// there was no default, which put a registry path in front of a customer who
// has no way to judge whether it is the right one.
func resolveLinkImage(envValue string) string {
	if v := strings.TrimSpace(envValue); v != "" {
		return v
	}
	return defaultLinkImage
}

// resolveNodeManagesLink decides whether this node runs its own Link sidecar.
//
// external is the first answer: nobody else deploys a Link onto a customer's
// machine, so a BYON/external node always manages its own. In the cluster,
// setting LINK_IMAGE is the opt-in - it has no other consumer, so a node that
// was handed an image is a node meant to manage its own Link.
//
// linkImageEnv is the RAW env value, never the resolved one. Passing the
// resolved image would make the fallback above answer "yes" for every node in
// the fleet, which is a much bigger change than a default image.
//
// The explicit flag remains the escape hatch for the one topology where an
// image is configured but an operator deploys Link separately - without it,
// both would fight over the single fixed-name dylaris_link container.
//
// A previous default followed external ALONE, which was wrong in a quiet way: a
// Link is needed per MC node whenever routing is gateway/both, not only for
// BYON - Core mints link credentials for every node at enroll. An in-cluster
// gateway node therefore came up with no Link and no error.
func resolveNodeManagesLink(envValue, linkImageEnv string, external bool) bool {
	if envValue != "" {
		return envValue == "true"
	}
	return external || strings.TrimSpace(linkImageEnv) != ""
}
