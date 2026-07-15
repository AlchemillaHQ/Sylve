// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"sort"
)

func normalizeDatasetOperationRoots(datasets []string) []string {
	normalized := make([]string, 0, len(datasets))
	seen := make(map[string]struct{})
	for _, dataset := range datasets {
		dataset = normalizeRestoreDestinationDataset(dataset)
		if dataset == "" {
			continue
		}
		if _, exists := seen[dataset]; exists {
			continue
		}
		seen[dataset] = struct{}{}
		normalized = append(normalized, dataset)
	}
	sort.Strings(normalized)

	// Holding an ancestor already covers every descendant in this operation.
	minimal := make([]string, 0, len(normalized))
	for _, dataset := range normalized {
		covered := false
		for _, ancestor := range minimal {
			if datasetWithinRoot(ancestor, dataset) {
				covered = true
				break
			}
		}
		if !covered {
			minimal = append(minimal, dataset)
		}
	}
	return minimal
}

func (s *Service) acquireDatasetOperations(datasets []string) (bool, string, []string) {
	roots := normalizeDatasetOperationRoots(datasets)
	if len(roots) == 0 {
		return false, "", nil
	}

	s.restoreDestinationMu.Lock()
	defer s.restoreDestinationMu.Unlock()
	if s.runningRestoreDestination == nil {
		s.runningRestoreDestination = make(map[string]struct{})
	}
	for _, requested := range roots {
		for existing := range s.runningRestoreDestination {
			if datasetWithinRoot(existing, requested) || datasetWithinRoot(requested, existing) {
				return false, existing, nil
			}
		}
	}
	for _, root := range roots {
		s.runningRestoreDestination[root] = struct{}{}
	}
	return true, "", roots
}

func (s *Service) releaseDatasetOperations(datasets []string) {
	roots := normalizeDatasetOperationRoots(datasets)
	if len(roots) == 0 {
		return
	}
	s.restoreDestinationMu.Lock()
	defer s.restoreDestinationMu.Unlock()
	for _, root := range roots {
		delete(s.runningRestoreDestination, root)
	}
}

// acquireDatasetOperationsWhileHolding atomically extends an existing
// destination lock to additional, unrelated dataset roots. Roots already
// covered by heldDataset are skipped and no partial acquisition occurs.
func (s *Service) acquireDatasetOperationsWhileHolding(
	heldDataset string,
	datasets []string,
) (bool, string, []string) {
	heldDataset = normalizeRestoreDestinationDataset(heldDataset)
	requestedRoots := normalizeDatasetOperationRoots(datasets)
	if heldDataset == "" || len(requestedRoots) == 0 {
		return false, "", nil
	}

	s.restoreDestinationMu.Lock()
	defer s.restoreDestinationMu.Unlock()
	if s.runningRestoreDestination == nil {
		return false, "", nil
	}
	if _, held := s.runningRestoreDestination[heldDataset]; !held {
		return false, "", nil
	}

	additional := make([]string, 0, len(requestedRoots))
	for _, requested := range requestedRoots {
		if datasetWithinRoot(heldDataset, requested) || datasetWithinRoot(requested, heldDataset) {
			continue
		}
		for existing := range s.runningRestoreDestination {
			if existing == heldDataset {
				continue
			}
			if datasetWithinRoot(existing, requested) || datasetWithinRoot(requested, existing) {
				return false, existing, nil
			}
		}
		additional = append(additional, requested)
	}

	for _, root := range additional {
		s.runningRestoreDestination[root] = struct{}{}
	}
	return true, "", additional
}
