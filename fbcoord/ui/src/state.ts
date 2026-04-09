export interface AppState {
  authenticated: boolean | null;
  pollIntervalMs: number;
  generatedToken: string | null;
  generatedNodeToken: {
    node_id: string;
    token: string;
  } | null;
  renderNonce: number;
}

export const appState: AppState = {
  authenticated: null,
  pollIntervalMs: 5000,
  generatedToken: null,
  generatedNodeToken: null,
  renderNonce: 0
};
