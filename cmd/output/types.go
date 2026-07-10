// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package output

// ClusterSyncStatus represents the sync outcome for a single cluster.
type ClusterSyncStatus string

const (
	ClusterSyncStatusSynced  ClusterSyncStatus = "synced"
	ClusterSyncStatusSkipped ClusterSyncStatus = "skipped"
	ClusterSyncStatusFailed  ClusterSyncStatus = "failed"
)

// ClusterSyncResult holds per-cluster sync information.
type ClusterSyncResult struct {
	Name    string            `json:"name"             yaml:"name"`
	Context string            `json:"context"          yaml:"context"`
	Status  ClusterSyncStatus `json:"status"           yaml:"status"`
	Reason  string            `json:"reason,omitempty" yaml:"reason,omitempty"`
}

// SyncResult is the top-level output of the sync command.
type SyncResult struct {
	Clusters []ClusterSyncResult `json:"clusters" yaml:"clusters"`
	Synced   int                 `json:"synced"   yaml:"synced"`
	Skipped  int                 `json:"skipped"  yaml:"skipped"`
	Failed   int                 `json:"failed"   yaml:"failed"`
}

// ClusterVersionResult is the output of the cluster-version command.
type ClusterVersionResult struct {
	Context string `json:"context" yaml:"context"`
	Version string `json:"version" yaml:"version"`
}

// VersionInfo is the output of the version command.
type VersionInfo struct {
	Version   string `json:"version"   yaml:"version"`
	GitCommit string `json:"gitCommit" yaml:"gitCommit"`
	BuildDate string `json:"buildDate" yaml:"buildDate"`
	GoVersion string `json:"goVersion" yaml:"goVersion"`
	Compiler  string `json:"compiler"  yaml:"compiler"`
	Platform  string `json:"platform"  yaml:"platform"`
}

// ErrorResult is the structured representation of a fatal command error.
// It is used when the output format is json or yaml so that errors are
// machine-parseable in the same way as successful output.
type ErrorResult struct {
	Error string `json:"error" yaml:"error"`
}

// AccessDiff describes one cluster access (context) that is changing.
type AccessDiff struct {
	Name       string        `json:"name"             yaml:"name"`
	ChangeType string        `json:"changeType"       yaml:"changeType"`
	Server     string        `json:"server,omitempty" yaml:"server,omitempty"`
	Fields     []FieldChange `json:"fields,omitempty" yaml:"fields,omitempty"`
}

// SyncDryRunResult is the output of `sync --dry-run`.
type SyncDryRunResult struct {
	Accesses  []AccessDiff `json:"accesses"  yaml:"accesses"`
	Clusters  []DiffEntry  `json:"clusters"  yaml:"clusters"`
	Contexts  []DiffEntry  `json:"contexts"  yaml:"contexts"`
	AuthInfos []DiffEntry  `json:"authInfos" yaml:"authInfos"`
	Added     int          `json:"added"     yaml:"added"`
	Removed   int          `json:"removed"   yaml:"removed"`
	Modified  int          `json:"modified"  yaml:"modified"`
}

// DiffEntry describes a single added, removed, or modified kubeconfig entry.
type DiffEntry struct {
	Name       string        `json:"name"             yaml:"name"`
	ChangeType string        `json:"changeType"       yaml:"changeType"`
	Fields     []FieldChange `json:"fields,omitempty" yaml:"fields,omitempty"`
}

// FieldChange describes a field-level change within a modified entry.
type FieldChange struct {
	Field string `json:"field" yaml:"field"`
	Old   string `json:"old"   yaml:"old"`
	New   string `json:"new"   yaml:"new"`
}
