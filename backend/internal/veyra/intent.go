package veyra

import "strings"

const (
	PortalIntentHome      = "home"
	PortalIntentSub2API   = "sub2api"
	PortalIntentAlchemy   = "alchemy"
	PortalIntentVideo     = "video"
	PortalIntentAggregate = "aggregate"
)

func NormalizePortalIntent(intent string) string {
	switch strings.ToLower(strings.TrimSpace(intent)) {
	case PortalIntentSub2API, PortalIntentAggregate:
		return PortalIntentSub2API
	case PortalIntentAlchemy:
		return PortalIntentAlchemy
	case PortalIntentVideo:
		return PortalIntentVideo
	default:
		return PortalIntentHome
	}
}
