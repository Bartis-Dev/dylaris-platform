package handlers

import (
	"testing"

	pb "dylaris-proto/node"
)

// metaJSON builds a .dylaris.json body the way the node writes it, so the test
// exercises the same shape subServerLoaderFromInspect unmarshals in production.
func metaJSON(subs ...struct{ name, typ, mc, build string }) string {
	out := `{"version":1,"sub_servers":[`
	for i, s := range subs {
		if i > 0 {
			out += ","
		}
		out += `{"name":"` + s.name + `","type":"` + s.typ + `","minecraft_version":"` + s.mc + `","build":"` + s.build + `"}`
	}
	return out + `]}`
}

func TestSubServerLoaderFromInspect(t *testing.T) {
	sub := func(name, typ, mc, build string) struct{ name, typ, mc, build string } {
		return struct{ name, typ, mc, build string }{name, typ, mc, build}
	}

	tests := []struct {
		name    string
		resp    *pb.InspectOrphanResp
		subName string
		wantIT  string
		wantMC  string
		wantB   string
		wantOK  bool
	}{
		{
			name: "metadata hit carries type, version and build",
			resp: &pb.InspectOrphanResp{
				HasMetadata:  true,
				MetadataJson: metaJSON(sub("survival", "paper", "1.20.1", "196")),
			},
			subName: "survival",
			wantIT:  "paper", wantMC: "1.20.1", wantB: "196", wantOK: true,
		},
		{
			name: "picks the requested sub-server, not the first",
			resp: &pb.InspectOrphanResp{
				HasMetadata: true,
				MetadataJson: metaJSON(
					sub("survival", "paper", "1.20.1", "196"),
					sub("modded", "neoforge", "1.21", ""),
				),
			},
			subName: "modded",
			wantIT:  "neoforge", wantMC: "1.21", wantB: "", wantOK: true,
		},
		{
			name: "metadata present but empty version/build returns honest empties",
			resp: &pb.InspectOrphanResp{
				HasMetadata:  true,
				MetadataJson: metaJSON(sub("survival", "vanilla", "", "")),
			},
			subName: "survival",
			wantIT:  "vanilla", wantMC: "", wantB: "", wantOK: true,
		},
		{
			name: "falls back to directory scan when metadata omits the sub-server",
			resp: &pb.InspectOrphanResp{
				HasMetadata:  true,
				MetadataJson: metaJSON(sub("survival", "paper", "1.20.1", "196")),
				SubServers:   []*pb.SubServerInfo{{Name: "creative", Type: "fabric"}},
			},
			subName: "creative",
			wantIT:  "fabric", wantMC: "", wantB: "", wantOK: true,
		},
		{
			name: "falls back to directory scan when metadata is malformed",
			resp: &pb.InspectOrphanResp{
				HasMetadata:  true,
				MetadataJson: "{not valid json",
				SubServers:   []*pb.SubServerInfo{{Name: "survival", Type: "spigot"}},
			},
			subName: "survival",
			wantIT:  "spigot", wantMC: "", wantB: "", wantOK: true,
		},
		{
			name: "no metadata, no scan match -> not ok, values untouched",
			resp: &pb.InspectOrphanResp{
				HasMetadata:  true,
				MetadataJson: metaJSON(sub("survival", "paper", "1.20.1", "196")),
			},
			subName: "ghost",
			wantIT:  "", wantMC: "", wantB: "", wantOK: false,
		},
		{
			name:    "nil response is not ok",
			resp:    nil,
			subName: "survival",
			wantOK:  false,
		},
		{
			name: "empty sub-server name is not ok",
			resp: &pb.InspectOrphanResp{
				HasMetadata:  true,
				MetadataJson: metaJSON(sub("survival", "paper", "1.20.1", "196")),
			},
			subName: "",
			wantOK:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			it, mc, b, ok := subServerLoaderFromInspect(tt.resp, tt.subName)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if it != tt.wantIT || mc != tt.wantMC || b != tt.wantB {
				t.Fatalf("got (%q,%q,%q), want (%q,%q,%q)", it, mc, b, tt.wantIT, tt.wantMC, tt.wantB)
			}
		})
	}
}
