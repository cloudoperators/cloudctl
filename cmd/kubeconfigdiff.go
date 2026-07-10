// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/cloudoperators/cloudctl/cmd/output"
)

// DiffChangeType describes the kind of change detected for a kubeconfig entry.
type DiffChangeType string

const (
	DiffChangeAdded    DiffChangeType = "added"
	DiffChangeRemoved  DiffChangeType = "removed"
	DiffChangeModified DiffChangeType = "modified"
)

// KubeconfigDiff holds the set of entry-level differences between two kubeconfigs.
// Only managed entries (those carrying the configured prefix) are included.
type KubeconfigDiff struct {
	Clusters  []EntryDiff
	Contexts  []EntryDiff
	AuthInfos []EntryDiff
}

// EntryDiff describes the diff for a single named kubeconfig entry.
type EntryDiff struct {
	Name       string
	ChangeType DiffChangeType
	Fields     []FieldDiff // non-empty only for DiffChangeModified
}

// FieldDiff is an internal field-level change, mapped to output.FieldChange for printing.
type FieldDiff struct {
	Field string
	Old   string
	New   string
}

// diffKubeconfig computes the diff between the old and new kubeconfig,
// restricting to entries whose names are managed (have the prefix).
func diffKubeconfig(oldCfg, newCfg *clientcmdapi.Config) KubeconfigDiff {
	var d KubeconfigDiff
	d.Clusters = diffClusters(oldCfg, newCfg)
	d.Contexts = diffContexts(oldCfg, newCfg)
	d.AuthInfos = diffAuthInfos(oldCfg, newCfg)
	return d
}

// diffClusters returns added/removed/modified cluster entries for managed names.
func diffClusters(oldCfg, newCfg *clientcmdapi.Config) []EntryDiff {
	var diffs []EntryDiff

	// Added or modified in new
	for name, newCluster := range newCfg.Clusters {
		if !isManaged(name) {
			continue
		}
		oldCluster, exists := oldCfg.Clusters[name]
		if !exists {
			diffs = append(diffs, EntryDiff{Name: name, ChangeType: DiffChangeAdded})
			continue
		}
		var fields []FieldDiff
		if oldCluster.Server != newCluster.Server {
			fields = append(fields, FieldDiff{Field: "Server", Old: oldCluster.Server, New: newCluster.Server})
		}
		if !bytes.Equal(oldCluster.CertificateAuthorityData, newCluster.CertificateAuthorityData) {
			oldFP := caFingerprint(oldCluster.CertificateAuthorityData)
			newFP := caFingerprint(newCluster.CertificateAuthorityData)
			fields = append(fields, FieldDiff{Field: "CA", Old: oldFP, New: newFP})
		}
		if !labelsExtensionEqual(oldCluster.Extensions, newCluster.Extensions) {
			oldLbl := string(extensionRaw(oldCluster.Extensions, "labels"))
			newLbl := string(extensionRaw(newCluster.Extensions, "labels"))
			fields = append(fields, FieldDiff{Field: "Labels", Old: oldLbl, New: newLbl})
		}
		if len(fields) > 0 {
			diffs = append(diffs, EntryDiff{Name: name, ChangeType: DiffChangeModified, Fields: fields})
		}
	}

	// Removed from old
	for name := range oldCfg.Clusters {
		if !isManaged(name) {
			continue
		}
		if _, exists := newCfg.Clusters[name]; !exists {
			diffs = append(diffs, EntryDiff{Name: name, ChangeType: DiffChangeRemoved})
		}
	}

	sort.Slice(diffs, func(i, j int) bool { return diffs[i].Name < diffs[j].Name })
	return diffs
}

// diffContexts returns added/removed/modified context entries for managed names.
func diffContexts(oldCfg, newCfg *clientcmdapi.Config) []EntryDiff {
	var diffs []EntryDiff

	for name, newCtx := range newCfg.Contexts {
		if !isManaged(name) {
			continue
		}
		oldCtx, exists := oldCfg.Contexts[name]
		if !exists {
			diffs = append(diffs, EntryDiff{Name: name, ChangeType: DiffChangeAdded})
			continue
		}
		var fields []FieldDiff
		if oldCtx.Cluster != newCtx.Cluster {
			fields = append(fields, FieldDiff{Field: "Cluster", Old: oldCtx.Cluster, New: newCtx.Cluster})
		}
		if oldCtx.AuthInfo != newCtx.AuthInfo {
			fields = append(fields, FieldDiff{Field: "AuthInfo", Old: oldCtx.AuthInfo, New: newCtx.AuthInfo})
		}
		if oldCtx.Namespace != newCtx.Namespace {
			fields = append(fields, FieldDiff{Field: "Namespace", Old: oldCtx.Namespace, New: newCtx.Namespace})
		}
		if len(fields) > 0 {
			diffs = append(diffs, EntryDiff{Name: name, ChangeType: DiffChangeModified, Fields: fields})
		}
	}

	for name := range oldCfg.Contexts {
		if !isManaged(name) {
			continue
		}
		if _, exists := newCfg.Contexts[name]; !exists {
			diffs = append(diffs, EntryDiff{Name: name, ChangeType: DiffChangeRemoved})
		}
	}

	sort.Slice(diffs, func(i, j int) bool { return diffs[i].Name < diffs[j].Name })
	return diffs
}

// diffAuthInfos returns added/removed/modified authinfo entries for managed names.
func diffAuthInfos(oldCfg, newCfg *clientcmdapi.Config) []EntryDiff {
	var diffs []EntryDiff

	for name, newAuth := range newCfg.AuthInfos {
		if !isManaged(name) {
			continue
		}
		oldAuth, exists := oldCfg.AuthInfos[name]
		if !exists {
			diffs = append(diffs, EntryDiff{Name: name, ChangeType: DiffChangeAdded})
			continue
		}
		if authInfoEqual(oldAuth, newAuth) {
			continue
		}
		var fields []FieldDiff
		// Exec-based diff
		if newAuth.Exec != nil && oldAuth.Exec != nil {
			fields = append(fields, argsDiff(oldAuth.Exec.Args, newAuth.Exec.Args)...)
		} else if newAuth.Exec != nil && oldAuth.Exec == nil {
			fields = append(fields, FieldDiff{Field: "Auth type", Old: "auth-provider", New: "exec-plugin"})
		} else if newAuth.Exec == nil && oldAuth.Exec != nil {
			fields = append(fields, FieldDiff{Field: "Auth type", Old: "exec-plugin", New: "auth-provider"})
		}
		// AuthProvider diff
		if newAuth.AuthProvider != nil && oldAuth.AuthProvider != nil {
			oldFiltered := filterAuthProviderConfig(oldAuth.AuthProvider.Config)
			newFiltered := filterAuthProviderConfig(newAuth.AuthProvider.Config)
			allKeys := make(map[string]struct{})
			for k := range oldFiltered {
				allKeys[k] = struct{}{}
			}
			for k := range newFiltered {
				allKeys[k] = struct{}{}
			}
			sortedKeys := make([]string, 0, len(allKeys))
			for k := range allKeys {
				sortedKeys = append(sortedKeys, k)
			}
			sort.Strings(sortedKeys)
			for _, k := range sortedKeys {
				ov := oldFiltered[k]
				nv := newFiltered[k]
				if ov != nv {
					fields = append(fields, FieldDiff{Field: fmt.Sprintf("auth-provider.%s", k), Old: ov, New: nv})
				}
			}
		}
		if len(fields) > 0 {
			diffs = append(diffs, EntryDiff{Name: name, ChangeType: DiffChangeModified, Fields: fields})
		} else {
			// authInfoEqual returned false but no specific fields identified — generic modified
			diffs = append(diffs, EntryDiff{Name: name, ChangeType: DiffChangeModified})
		}
	}

	for name := range oldCfg.AuthInfos {
		if !isManaged(name) {
			continue
		}
		if _, exists := newCfg.AuthInfos[name]; !exists {
			diffs = append(diffs, EntryDiff{Name: name, ChangeType: DiffChangeRemoved})
		}
	}

	sort.Slice(diffs, func(i, j int) bool { return diffs[i].Name < diffs[j].Name })
	return diffs
}

// argsDiff computes per-argument differences between two exec arg lists, returning
// FieldDiff entries for args that appear only in old (removed) or only in new (added).
func argsDiff(oldArgs, newArgs []string) []FieldDiff {
	oldSet := make(map[string]bool, len(oldArgs))
	for _, a := range oldArgs {
		oldSet[a] = true
	}
	newSet := make(map[string]bool, len(newArgs))
	for _, a := range newArgs {
		newSet[a] = true
	}

	var removed, added []string
	for _, a := range oldArgs {
		if !newSet[a] {
			removed = append(removed, a)
		}
	}
	for _, a := range newArgs {
		if !oldSet[a] {
			added = append(added, a)
		}
	}

	var diffs []FieldDiff
	for _, r := range removed {
		diffs = append(diffs, FieldDiff{Field: "Exec Args", Old: r, New: ""})
	}
	for _, a := range added {
		diffs = append(diffs, FieldDiff{Field: "Exec Args", Old: "", New: a})
	}
	return diffs
}

// caFingerprint returns the first 16 hex characters of the SHA-256 hash of ca data.
func caFingerprint(data []byte) string {
	if len(data) == 0 {
		return "<empty>"
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])[:16]
}

// toOutputDiffEntries converts internal EntryDiff slice to output.DiffEntry slice.
func toOutputDiffEntries(diffs []EntryDiff) []output.DiffEntry {
	result := make([]output.DiffEntry, 0, len(diffs))
	for _, d := range diffs {
		entry := output.DiffEntry{
			Name:       d.Name,
			ChangeType: string(d.ChangeType),
		}
		for _, f := range d.Fields {
			entry.Fields = append(entry.Fields, output.FieldChange{
				Field: f.Field,
				Old:   f.Old,
				New:   f.New,
			})
		}
		result = append(result, entry)
	}
	return result
}

// buildDryRunResult converts a KubeconfigDiff to an output.SyncDryRunResult.
func buildDryRunResult(diff KubeconfigDiff) output.SyncDryRunResult {
	var added, removed, modified int
	countChanges := func(entries []EntryDiff) {
		for _, e := range entries {
			switch e.ChangeType {
			case DiffChangeAdded:
				added++
			case DiffChangeRemoved:
				removed++
			case DiffChangeModified:
				modified++
			}
		}
	}
	countChanges(diff.Clusters)
	countChanges(diff.Contexts)
	countChanges(diff.AuthInfos)

	clusters := toOutputDiffEntries(diff.Clusters)
	contexts := toOutputDiffEntries(diff.Contexts)
	authInfos := toOutputDiffEntries(diff.AuthInfos)

	// Use empty slices instead of nil for consistent JSON/YAML output
	if clusters == nil {
		clusters = []output.DiffEntry{}
	}
	if contexts == nil {
		contexts = []output.DiffEntry{}
	}
	if authInfos == nil {
		authInfos = []output.DiffEntry{}
	}

	return output.SyncDryRunResult{
		Clusters:  clusters,
		Contexts:  contexts,
		AuthInfos: authInfos,
		Added:     added,
		Removed:   removed,
		Modified:  modified,
	}
}
