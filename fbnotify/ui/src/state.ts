export interface AppState {
  authenticated: boolean | null;
  generatedToken: string | null;
  generatedNodeToken: {
    key_id: string;
    token: string;
    source_service: string;
    source_instance: string;
  } | null;
  editingTargetId: string | null;
  editingRouteId: string | null;
  renderNonce: number;
}

export const appState: AppState = {
  authenticated: null,
  generatedToken: null,
  generatedNodeToken: null,
  editingTargetId: null,
  editingRouteId: null,
  renderNonce: 0
};
