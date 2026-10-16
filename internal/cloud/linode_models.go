package cloud

import "context"

// LinodeAdvanced is an optional provider seam; other clouds keep their existing API.
type LinodeAdvanced interface {
	LinodeRead(context.Context, string, string, string, int) (any, error)
	ValidateChange(context.Context, string, string, LinodeChange) error
	Change(context.Context, string, string, LinodeChange, string) (Operation, error)
	ValidateFirewallSelection(context.Context, FirewallSelection) error
}

type LinodeChange struct {
	Startup              *StartupInput      `json:"startup,omitempty"`
	MetadataAcknowledged bool               `json:"metadataAcknowledged,omitempty"`
	Snapshot             string             `json:"snapshot"`
	BindingSnapshot      string             `json:"bindingSnapshot,omitempty"`
	Image                string             `json:"image,omitempty"`
	Password             string             `json:"password,omitempty"`
	Key                  string             `json:"key,omitempty"`
	SSHPublicKey         string             `json:"sshPublicKey,omitempty"`
	Plan                 string             `json:"plan,omitempty"`
	Region               string             `json:"region,omitempty"`
	Label                string             `json:"label,omitempty"`
	DiskID               int                `json:"diskId,omitempty"`
	InterfaceID          int                `json:"interfaceId,omitempty"`
	Firewall             *FirewallSelection `json:"firewall,omitempty"`
	Rules                *FirewallRules     `json:"rules,omitempty"`
	Status               string             `json:"status,omitempty"`
	Acknowledge          bool               `json:"acknowledge"`
}
type FirewallSelection struct {
	Snapshot        string         `json:"snapshot,omitempty"`
	Mode            string         `json:"mode"`
	ID              int            `json:"id,omitempty"`
	Rules           *FirewallRules `json:"rules,omitempty"`
	AllowUnverified bool           `json:"allowUnverified,omitempty"`
}
type FirewallRules struct {
	Inbound        []FirewallRule `json:"inbound"`
	Outbound       []FirewallRule `json:"outbound"`
	InboundPolicy  string         `json:"inbound_policy"`
	OutboundPolicy string         `json:"outbound_policy"`
}
type FirewallRule struct {
	Action    string            `json:"action"`
	Protocol  string            `json:"protocol"`
	Ports     string            `json:"ports,omitempty"`
	Label     string            `json:"label,omitempty"`
	Addresses FirewallAddresses `json:"addresses"`
}
type FirewallAddresses struct {
	IPv4 []string `json:"ipv4,omitempty"`
	IPv6 []string `json:"ipv6,omitempty"`
}
type LinodeDisk struct {
	ID         int    `json:"id"`
	Label      string `json:"label"`
	Filesystem string `json:"filesystem"`
	Size       int    `json:"size"`
	Status     string `json:"status"`
}
type LinodeInterface struct {
	ID           int            `json:"id"`
	Public       map[string]any `json:"public,omitempty"`
	VPC          map[string]any `json:"vpc,omitempty"`
	VLAN         map[string]any `json:"vlan,omitempty"`
	DefaultRoute map[string]any `json:"default_route,omitempty"`
}
type LinodeConfig struct {
	ID      int    `json:"id"`
	Label   string `json:"label"`
	Devices map[string]struct {
		DiskID   int `json:"disk_id"`
		VolumeID int `json:"volume_id"`
	} `json:"devices"`
	Interfaces []struct {
		Purpose string `json:"purpose"`
		VPCID   int    `json:"vpc_id"`
		Label   string `json:"label"`
	} `json:"interfaces"`
}
type LinodeState struct {
	HasUserData    *bool             `json:"hasUserData"`
	Instance       Instance          `json:"instance"`
	Generation     string            `json:"generation"`
	Locks          []string          `json:"locks"`
	PlacementGroup bool              `json:"placementGroup"`
	Managed        bool              `json:"managed"`
	Disks          []LinodeDisk      `json:"disks"`
	Configs        []LinodeConfig    `json:"configs"`
	Interfaces     []LinodeInterface `json:"interfaces"`
	Volumes        []struct {
		ID    int    `json:"id"`
		Label string `json:"label"`
		Size  int    `json:"size"`
	} `json:"volumes"`
	Snapshot  string `json:"snapshot"`
	QueriedAt int64  `json:"queriedAt"`
}
type LinodeFirewall struct {
	ID       int              `json:"id"`
	Label    string           `json:"label"`
	Status   string           `json:"status"`
	Rules    FirewallRules    `json:"rules"`
	Devices  []FirewallDevice `json:"devices"`
	Snapshot string           `json:"snapshot"`
}
type FirewallDevice struct {
	ID     int `json:"id"`
	Entity struct {
		ID           int    `json:"id"`
		Type         string `json:"type"`
		Label        string `json:"label"`
		URL          string `json:"url"`
		ParentEntity *struct {
			ID   int    `json:"id"`
			Type string `json:"type"`
		} `json:"parent_entity,omitempty"`
	} `json:"entity"`
}
type LinodeFirewallView struct {
	Items     []LinodeFirewall `json:"items"`
	Snapshot  string           `json:"snapshot"`
	QueriedAt int64            `json:"queriedAt"`
}

type LinodeOperation struct {
	Action           string             `json:"action"`
	Steps            []string           `json:"steps,omitempty"`
	Configured       bool               `json:"configured,omitempty"`
	Plan             string             `json:"plan,omitempty"`
	Image            string             `json:"image,omitempty"`
	Label            string             `json:"label,omitempty"`
	SourceID         string             `json:"sourceId,omitempty"`
	Firewall         *FirewallSelection `json:"firewall,omitempty"`
	InterfaceID      int                `json:"interfaceId,omitempty"`
	Status           string             `json:"status,omitempty"`
	EventAfter       int                `json:"eventAfter,omitempty"`
	DiskID           int                `json:"diskId,omitempty"`
	ExpectedRules    *FirewallRules     `json:"expectedRules,omitempty"`
	ExpectedStatus   string             `json:"expectedStatus,omitempty"`
	FirewallSnapshot string             `json:"firewallSnapshot,omitempty"`
	StartedAt        int64              `json:"startedAt,omitempty"`
}
