// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/cloudoperators/cloudctl/cmd/output"
)

// sensitiveAuthProviderKeys are auth-provider config keys whose values must
// not appear in plain or structured diff output.
var sensitiveAuthProviderKeys = map[string]bool{
	"client-secret": true,
}

// sensitiveArgPrefixes are kubelogin (and similar) flag prefixes whose values
// must not appear verbatim in diff output.
var sensitiveArgPrefixes = []string{
	"--oidc-client-secret=",
}

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

// isManagedContext returns true if the context at name references a managed cluster
// (i.e. a cluster whose name has the managed prefix). Context names themselves are
// stored without the prefix, so we check the cluster reference instead.
func isManagedContext(name string, oldCfg, newCfg *clientcmdapi.Config) bool {
	if ctx, ok := newCfg.Contexts[name]; ok {
		return isManaged(ctx.Cluster)
	}
	if ctx, ok := oldCfg.Contexts[name]; ok {
		return isManaged(ctx.Cluster)
	}
	return false
}

// diffContexts returns added/removed/modified context entries for managed contexts.
// A context is considered managed if its cluster reference carries the managed prefix.
func diffContexts(oldCfg, newCfg *clientcmdapi.Config) []EntryDiff {
	var diffs []EntryDiff

	for name, newCtx := range newCfg.Contexts {
		if !isManagedContext(name, oldCfg, newCfg) {
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
		if !isManagedContext(name, oldCfg, newCfg) {
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
					if sensitiveAuthProviderKeys[k] {
						ov, nv = "<redacted>", "<redacted>"
					}
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

// redactArg replaces the value portion of a sensitive flag with <redacted>,
// leaving the flag name intact for readability (e.g. "--oidc-client-secret=<redacted>").
func redactArg(arg string) string {
	for _, pfx := range sensitiveArgPrefixes {
		if strings.HasPrefix(arg, pfx) {
			return pfx + "<redacted>"
		}
	}
	return arg
}

// argsDiff computes per-argument differences between two exec arg lists, returning
// FieldDiff entries for args that appear only in old (removed) or only in new (added).
// Values of sensitive flags (e.g. --oidc-client-secret=) are redacted.
// When a sensitive flag appears in both removed and added (value changed), a single
// entry with Old and New both set to "<redacted>" is emitted rather than separate lines.
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

	// Pair up sensitive flag changes: if the same prefix appears in both removed
	// and added, the value changed — emit a single modified entry instead of
	// separate remove+add lines that both redact to the same visible string.
	pairedPrefixes := make(map[string]bool)
	for _, pfx := range sensitiveArgPrefixes {
		var inRemoved, inAdded bool
		for _, r := range removed {
			if strings.HasPrefix(r, pfx) {
				inRemoved = true
				break
			}
		}
		for _, a := range added {
			if strings.HasPrefix(a, pfx) {
				inAdded = true
				break
			}
		}
		if inRemoved && inAdded {
			pairedPrefixes[pfx] = true
		}
	}

	var diffs []FieldDiff
	// Emit paired sensitive changes as a single modified entry.
	for pfx := range pairedPrefixes {
		diffs = append(diffs, FieldDiff{Field: "Exec Args", Old: pfx + "<redacted>", New: pfx + "<redacted>"})
	}
	for _, r := range removed {
		isPaired := false
		for pfx := range pairedPrefixes {
			if strings.HasPrefix(r, pfx) {
				isPaired = true
				break
			}
		}
		if !isPaired {
			diffs = append(diffs, FieldDiff{Field: "Exec Args", Old: redactArg(r), New: ""})
		}
	}
	for _, a := range added {
		isPaired := false
		for pfx := range pairedPrefixes {
			if strings.HasPrefix(a, pfx) {
				isPaired = true
				break
			}
		}
		if !isPaired {
			diffs = append(diffs, FieldDiff{Field: "Exec Args", Old: "", New: redactArg(a)})
		}
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

// buildAccessDiffs derives a []output.AccessDiff from the context-level diff,
// enriching each entry with the server URL (looked up from the cluster the
// context references) and credential-change information.
// It also synthesises modified access entries for contexts whose underlying
// cluster changed server URL or CA (tracked in diff.Clusters) even when the
// context itself was not structurally modified.
func buildAccessDiffs(diff KubeconfigDiff, oldCfg, newCfg *clientcmdapi.Config) []output.AccessDiff {
	// accesses keyed by context name to allow merging
	type accessEntry struct {
		access  output.AccessDiff
		fromCtx bool // originated from a context-level diff
	}
	byName := make(map[string]*accessEntry)

	// 1. Process explicit context-level diffs (added / removed / modified context refs)
	for _, ctxDiff := range diff.Contexts {
		name := ctxDiff.Name
		entry := &accessEntry{
			access: output.AccessDiff{
				Name:       name,
				ChangeType: string(ctxDiff.ChangeType),
			},
			fromCtx: true,
		}

		switch ctxDiff.ChangeType {
		case DiffChangeAdded:
			if ctx, ok := newCfg.Contexts[name]; ok {
				if cluster, ok := newCfg.Clusters[ctx.Cluster]; ok {
					entry.access.Server = cluster.Server
				}
			}
		case DiffChangeRemoved:
			if ctx, ok := oldCfg.Contexts[name]; ok {
				if cluster, ok := oldCfg.Clusters[ctx.Cluster]; ok {
					entry.access.Server = cluster.Server
				}
			}
		case DiffChangeModified:
			oldCtx := oldCfg.Contexts[name]
			newCtx := newCfg.Contexts[name]
			if oldCtx == nil || newCtx == nil {
				break
			}
			// Server URL change (resolve through cluster refs)
			oldCluster := oldCfg.Clusters[oldCtx.Cluster]
			newCluster := newCfg.Clusters[newCtx.Cluster]
			oldServer, newServer := "", ""
			if oldCluster != nil {
				oldServer = oldCluster.Server
			}
			if newCluster != nil {
				newServer = newCluster.Server
			}
			if oldServer != newServer {
				entry.access.Fields = append(entry.access.Fields, output.FieldChange{
					Field: "Server",
					Old:   oldServer,
					New:   newServer,
				})
			}
			// Carry through any credential field diffs from the authinfo diff
			// (exec args, auth-provider config). If the auth objects are simply
			// absent or unequal with no specific fields, fall back to the
			// generic sentinel.
			oldAuth := oldCfg.AuthInfos[oldCtx.AuthInfo]
			newAuth := newCfg.AuthInfos[newCtx.AuthInfo]
			credChanged := (oldAuth != nil && newAuth != nil && !authInfoEqual(oldAuth, newAuth)) ||
				(oldAuth == nil) != (newAuth == nil)
			if credChanged {
				// Try to produce specific field-level diffs by comparing the
				// auth objects directly, the same way diffAuthInfos does.
				var authFields []output.FieldChange
				if oldAuth != nil && newAuth != nil {
					if newAuth.Exec != nil && oldAuth.Exec != nil {
						for _, fd := range argsDiff(oldAuth.Exec.Args, newAuth.Exec.Args) {
							authFields = append(authFields, output.FieldChange{
								Field: fd.Field,
								Old:   fd.Old,
								New:   fd.New,
							})
						}
					} else if newAuth.Exec != nil && oldAuth.Exec == nil {
						authFields = append(authFields, output.FieldChange{Field: "Auth type", Old: "auth-provider", New: "exec-plugin"})
					} else if newAuth.Exec == nil && oldAuth.Exec != nil {
						authFields = append(authFields, output.FieldChange{Field: "Auth type", Old: "exec-plugin", New: "auth-provider"})
					}
					if newAuth.AuthProvider != nil && oldAuth.AuthProvider != nil {
						oldFiltered := filterAuthProviderConfig(oldAuth.AuthProvider.Config)
						newFiltered := filterAuthProviderConfig(newAuth.AuthProvider.Config)
						for _, k := range sortedKeys(oldFiltered, newFiltered) {
							ov, nv := oldFiltered[k], newFiltered[k]
							if ov != nv {
								if sensitiveAuthProviderKeys[k] {
									ov, nv = "<redacted>", "<redacted>"
								}
								authFields = append(authFields, output.FieldChange{
									Field: fmt.Sprintf("auth-provider.%s", k),
									Old:   ov,
									New:   nv,
								})
							}
						}
					}
				}
				if len(authFields) > 0 {
					entry.access.Fields = append(entry.access.Fields, authFields...)
				} else {
					entry.access.Fields = append(entry.access.Fields, output.FieldChange{
						Field: "Credentials",
						Old:   "changed",
						New:   "",
					})
				}
			}
		}
		// Skip modified entries that have no user-visible field changes —
		// these arise from internal authinfo hash reassignment during
		// deduplication where the effective credentials are unchanged.
		if ctxDiff.ChangeType == DiffChangeModified && len(entry.access.Fields) == 0 {
			continue
		}
		byName[name] = entry
	}

	// 2. For each modified cluster, find contexts in newCfg that reference it.
	// Merge cluster field changes (Server, CA, Labels) into the access entry,
	// creating a new entry if the context was not already captured in step 1.
	modifiedClusters := make(map[string]EntryDiff, len(diff.Clusters))
	for _, cd := range diff.Clusters {
		if cd.ChangeType == DiffChangeModified {
			modifiedClusters[cd.Name] = cd
		}
	}
	if len(modifiedClusters) > 0 {
		for ctxName, ctx := range newCfg.Contexts {
			clusterDiff, affected := modifiedClusters[ctx.Cluster]
			if !affected {
				continue
			}
			var fields []output.FieldChange
			for _, f := range clusterDiff.Fields {
				fields = append(fields, output.FieldChange{
					Field: f.Field,
					Old:   f.Old,
					New:   f.New,
				})
			}
			if len(fields) == 0 {
				continue
			}
			if existing, ok := byName[ctxName]; ok {
				// Merge cluster fields into the existing entry.
				existing.access.Fields = append(fields, existing.access.Fields...)
			} else {
				byName[ctxName] = &accessEntry{
					access: output.AccessDiff{
						Name:       ctxName,
						ChangeType: string(DiffChangeModified),
						Fields:     fields,
					},
				}
			}
		}
	}

	// 3. For each modified authinfo, find contexts in newCfg that reference it.
	// Carry the actual field diffs (exec args, auth-provider config) through so
	// diff format can show real old/new values. Merge into an existing entry when
	// one was already created by step 1 or 2, so credential changes are never
	// silently dropped. Fall back to a generic "Credentials: changed" entry only
	// when no specific fields were identified.
	modifiedAuthInfos := make(map[string]EntryDiff, len(diff.AuthInfos))
	for _, ad := range diff.AuthInfos {
		if ad.ChangeType == DiffChangeModified {
			modifiedAuthInfos[ad.Name] = ad
		}
	}
	if len(modifiedAuthInfos) > 0 {
		for ctxName, ctx := range newCfg.Contexts {
			ad, affected := modifiedAuthInfos[ctx.AuthInfo]
			if !affected {
				continue
			}
			var fields []output.FieldChange
			for _, f := range ad.Fields {
				fields = append(fields, output.FieldChange{
					Field: f.Field,
					Old:   f.Old,
					New:   f.New,
				})
			}
			if len(fields) == 0 {
				// authInfoEqual returned false but no specific fields were identified.
				fields = []output.FieldChange{{Field: "Credentials", Old: "changed", New: ""}}
			}
			if existing, ok := byName[ctxName]; ok {
				// Merge credential fields into the existing entry.
				existing.access.Fields = append(existing.access.Fields, fields...)
			} else {
				byName[ctxName] = &accessEntry{
					access: output.AccessDiff{
						Name:       ctxName,
						ChangeType: string(DiffChangeModified),
						Fields:     fields,
					},
				}
			}
		}
	}

	// Flatten and sort
	accesses := make([]output.AccessDiff, 0, len(byName))
	for _, e := range byName {
		accesses = append(accesses, e.access)
	}
	sort.Slice(accesses, func(i, j int) bool { return accesses[i].Name < accesses[j].Name })
	return accesses
}

// buildDryRunResult converts a KubeconfigDiff to an output.SyncDryRunResult.
// oldCfg and newCfg are needed to build the context-centric AccessDiff entries.
func buildDryRunResult(diff KubeconfigDiff, oldCfg, newCfg *clientcmdapi.Config) output.SyncDryRunResult {
	accesses := buildAccessDiffs(diff, oldCfg, newCfg)

	var added, removed, modified int
	for _, a := range accesses {
		switch DiffChangeType(a.ChangeType) {
		case DiffChangeAdded:
			added++
		case DiffChangeRemoved:
			removed++
		case DiffChangeModified:
			modified++
		}
	}

	clusters := toOutputDiffEntries(diff.Clusters)
	contexts := toOutputDiffEntries(diff.Contexts)
	authInfos := toOutputDiffEntries(diff.AuthInfos)

	// Use empty slices instead of nil for consistent JSON/YAML output
	if accesses == nil {
		accesses = []output.AccessDiff{}
	}
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
		Accesses:  accesses,
		Clusters:  clusters,
		Contexts:  contexts,
		AuthInfos: authInfos,
		Added:     added,
		Removed:   removed,
		Modified:  modified,
	}
}

// sortedKeys returns the union of keys from two string maps, sorted.
func sortedKeys(a, b map[string]string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
