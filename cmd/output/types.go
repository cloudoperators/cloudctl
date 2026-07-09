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
