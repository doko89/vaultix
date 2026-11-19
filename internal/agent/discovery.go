package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/moby/moby/client"
)

// DockerLister is the narrow read-only slice of the Docker Engine client used
// for discovery. *client.Client satisfies it; tests supply fakes.
// No privileged or mutating operations are permitted through this interface.
type DockerLister interface {
	ContainerList(ctx context.Context, options client.ContainerListOptions) (client.ContainerListResult, error)
	VolumeList(ctx context.Context, options client.VolumeListOptions) (client.VolumeListResult, error)
}

// DiscoveredContainer is the reported subset of container metadata.
type DiscoveredContainer struct {
	ID     string            `json:"id"`
	Names  []string          `json:"names"`
	Image  string            `json:"image"`
	State  string            `json:"state"`
	Status string            `json:"status"`
	Labels map[string]string `json:"labels"`
	Mounts []DiscoveredMount `json:"mounts,omitempty"`
}

// DiscoveredMount links a container to a volume or bind path.
type DiscoveredMount struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source,omitempty"`
	Destination string `json:"destination"`
}

// DiscoveredVolume is the reported subset of named-volume metadata.
type DiscoveredVolume struct {
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	Mountpoint string            `json:"mountpoint"`
	Labels     map[string]string `json:"labels,omitempty"`
}

// DiscoveryReport is one discovery pass. Disabled is true when Docker
// integration is switched off; Containers and Volumes are then empty.
type DiscoveryReport struct {
	Disabled   bool                  `json:"disabled"`
	At         time.Time             `json:"at"`
	Containers []DiscoveredContainer `json:"containers"`
	Volumes    []DiscoveredVolume    `json:"volumes"`
}

// Discoverer lists containers and volumes when enabled. Discovery is
// informational only; nothing is backed up unless a policy selects it.
type Discoverer struct {
	enabled bool
	lister  DockerLister
	timeout time.Duration
}

// NewDiscoverer builds a discoverer. When enabled is false the lister may be
// nil and Discover short-circuits without touching Docker.
func NewDiscoverer(enabled bool, lister DockerLister, timeout time.Duration) *Discoverer {
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	return &Discoverer{enabled: enabled, lister: lister, timeout: timeout}
}

// NewMobyClient dials nothing yet; it only constructs the Engine client for
// host (empty host means DOCKER_HOST / default socket) with API negotiation.
func NewMobyClient(host string) (*client.Client, error) {
	opts := []client.Opt{client.WithAPIVersionNegotiation(), client.WithAPIVersionFromEnv()}
	if host != "" {
		opts = append(opts, client.WithHost(host))
	} else {
		opts = append(opts, client.FromEnv)
	}
	cli, err := client.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("agent: create docker client: %w", err)
	}
	return cli, nil
}

// Discover lists all containers (running and stopped) plus named volumes.
// Each Engine call carries its own timeout.
func (d *Discoverer) Discover(ctx context.Context) (DiscoveryReport, error) {
	report := DiscoveryReport{At: time.Now().UTC()}
	if !d.enabled {
		report.Disabled = true
		return report, nil
	}
	if d.lister == nil {
		return report, fmt.Errorf("agent: docker discovery enabled but no client configured")
	}

	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	ctrs, err := d.lister.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return report, fmt.Errorf("agent: list containers: %w", err)
	}
	vols, err := d.lister.VolumeList(ctx, client.VolumeListOptions{})
	if err != nil {
		return report, fmt.Errorf("agent: list volumes: %w", err)
	}

	for _, c := range ctrs.Items {
		dc := DiscoveredContainer{
			ID:     c.ID,
			Names:  c.Names,
			Image:  c.Image,
			State:  string(c.State),
			Status: c.Status,
			Labels: c.Labels,
		}
		for _, m := range c.Mounts {
			dc.Mounts = append(dc.Mounts, DiscoveredMount{
				Type:        string(m.Type),
				Name:        m.Name,
				Source:      m.Source,
				Destination: m.Destination,
			})
		}
		report.Containers = append(report.Containers, dc)
	}
	for _, v := range vols.Items {
		report.Volumes = append(report.Volumes, DiscoveredVolume{
			Name:       v.Name,
			Driver:     v.Driver,
			Mountpoint: v.Mountpoint,
			Labels:     v.Labels,
		})
	}
	return report, nil
}
