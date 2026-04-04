export interface AppState {
  authenticated: boolean | null;
  pollIntervalMs: number;
  generatedToken: string | null;
  renderNonce: number;
}

export const appState: AppState = {
  authenticated: null,
  pollIntervalMs: 5000,
  generatedToken: null,
  renderNonce: 0
};
