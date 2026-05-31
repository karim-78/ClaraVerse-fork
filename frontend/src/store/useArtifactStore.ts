/**
 * Artifact Store - State Management for Artifact Pane
 *
 * Manages the artifact pane visibility, selected artifacts, and split ratio.
 * Uses Zustand with localStorage persistence for split ratio preference.
 */

import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import type { Artifact } from '@/types/artifact';

interface ArtifactState {
  /** Whether the artifact pane is open */
  isOpen: boolean;

  /** Array of artifacts to display */
  artifacts: Artifact[];

  /** Index of the currently selected artifact (for tab navigation) */
  selectedIndex: number;

  /** Split ratio between chat and artifact pane (0-100, represents chat pane %) */
  splitRatio: number;

  // Actions
  /** Open the artifact pane with the given artifacts */
  openArtifacts: (artifacts: Artifact[]) => void;

  /** Close the artifact pane */
  closePane: () => void;

  /** Select a specific artifact by index */
  selectArtifact: (index: number) => void;

  /** Update the split ratio */
  setSplitRatio: (ratio: number) => void;

  /** Add a new artifact to the current list */
  addArtifact: (artifact: Artifact) => void;

  /** Clear all artifacts */
  clearArtifacts: () => void;
}

export const useArtifactStore = create<ArtifactState>()(
  persist(
    set => ({
      isOpen: false,
      artifacts: [],
      selectedIndex: 0,
      splitRatio: 50, // Default: 50% chat, 50% artifact (balanced split)

      openArtifacts: artifacts => {
        console.log('🎨 Opening artifact pane with', artifacts.length, 'artifact(s)');
        set(state => ({
          isOpen: true,
          artifacts,
          selectedIndex: 0,
          // Auto-adjust to 50/50 split when opening, unless user has customized
          splitRatio: state.splitRatio > 60 ? 50 : state.splitRatio,
        }));
      },

      closePane: () =>
        set({
          isOpen: false,
        }),

      selectArtifact: index =>
        set(state => ({
          selectedIndex: Math.max(0, Math.min(index, state.artifacts.length - 1)),
        })),

      setSplitRatio: ratio =>
        set({
          splitRatio: Math.max(30, Math.min(ratio, 80)), // Clamp between 30-80%
        }),

      addArtifact: artifact =>
        set(state => ({
          artifacts: [...state.artifacts, artifact],
          isOpen: true,
        })),

      clearArtifacts: () =>
        set({
          artifacts: [],
          selectedIndex: 0,
          isOpen: false,
        }),
    }),
    {
      name: 'claraverse-artifact-store',
      // Only persist split ratio preference
      partialize: state => ({
        splitRatio: state.splitRatio,
      }),
    }
  )
);
