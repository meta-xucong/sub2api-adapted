package veyra

import "strings"

const (
	PortalIntentHome      = "home"
	PortalIntentSub2API   = "sub2api"
	PortalIntentAlchemy   = "alchemy"
	PortalIntentAggregate = "aggregate"
)

func NormalizePortalIntent(intent string) string {
	switch strings.ToLower(strings.TrimSpace(intent)) {
	case PortalIntentSub2API, PortalIntentAggregate:
		return PortalIntentSub2API
	case PortalIntentAlchemy:
		return PortalIntentAlchemy
	default:
		return PortalIntentHome
	}
}
