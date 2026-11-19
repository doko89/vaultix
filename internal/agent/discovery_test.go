package agent

import (
	"context"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeLister struct {
	containers []container.Summary
	volumes    []volume.Volume
	calls      int
}

func (f *fakeLister) ContainerList(context.Context, client.ContainerListOptions) (client.ContainerListResult, error) {
	f.calls++
	return client.ContainerListResult{Items: f.containers}, nil
}

func (f *fakeLister) VolumeList(context.Context, client.VolumeListOptions) (client.VolumeListResult, error) {
	f.calls++
	return client.VolumeListResult{Items: f.volumes}, nil
}

func TestDiscoverDisabled(t *testing.T) {
	d := NewDiscoverer(false, nil, time.Second)
	report, err := d.Discover(context.Background())
	require.NoError(t, err)
	assert.True(t, report.Disabled)
	assert.Empty(t, report.Containers)
	assert.Empty(t, report.Volumes)
}

func TestDiscoverListsContainersAndVolumes(t *testing.T) {
	fake := &fakeLister{
		containers: []container.Summary{{
			ID: "/abc123", Names: []string{"/web"}, Image: "nginx:latest",
			State: container.StateRunning, Status: "Up 2h",
			Labels: map[string]string{"com.docker.compose.project": "shop"},
		}},
		volumes: []volume.Volume{{
			Name: "shop_data", Driver: "local", Mountpoint: "/var/lib/docker/volumes/shop_data",
		}},
	}
	d := NewDiscoverer(true, fake, time.Second)
	report, err := d.Discover(context.Background())
	require.NoError(t, err)
	assert.False(t, report.Disabled)
	require.Len(t, report.Containers, 1)
	assert.Equal(t, "running", report.Containers[0].State)
	assert.Equal(t, "nginx:latest", report.Containers[0].Image)
	require.Len(t, report.Volumes, 1)
	assert.Equal(t, "shop_data", report.Volumes[0].Name)
	assert.Equal(t, 2, fake.calls, "one containers call + one volumes call")
}

func TestDiscoverEnabledWithoutClient(t *testing.T) {
	d := NewDiscoverer(true, nil, time.Second)
	_, err := d.Discover(context.Background())
	require.ErrorContains(t, err, "no client configured")
}
