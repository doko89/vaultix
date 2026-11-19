package discovery

// Well-known Detail map keys for discovered resources (PRD §7.3). Agents
// populate these; the Manager treats Detail as opaque metadata and never
// acts on it beyond display and policy selectors.
const (
	// Containers (ResourceKindDockerContainer).
	DetailContainerID = "container_id"
	DetailImage       = "image"
	DetailImageDigest = "image_digest"
	DetailState       = "state"
	DetailStatus      = "status"
	// DetailMounts is a JSON array of {type,name,source,destination}.
	DetailMounts = "mounts"

	// Named volumes (ResourceKindDockerVolume).
	DetailVolumeDriver = "driver"
	DetailMountpoint   = "mountpoint"

	// Compose (ResourceKindComposeProject).
	DetailComposeProject = "compose_project"
	DetailComposeFile    = "compose_file"

	// Host paths and mounts (ResourceKindHostPath, ResourceKindMount).
	DetailFilesystem   = "filesystem"
	DetailCapacityByte = "capacity_bytes"
	DetailFreeBytes    = "free_bytes"
	DetailReadOnly     = "read_only"

	// LabelPrefix namespaces container labels: "label.<key>".
	LabelPrefix = "label."
)
