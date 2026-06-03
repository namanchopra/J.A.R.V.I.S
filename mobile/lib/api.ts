import { storage } from './storage';

// ---------------------------------------------------------------------------
// Type definitions (matching Go backend)
// ---------------------------------------------------------------------------

export interface Session {
  id: string;
  taskId?: string;
  pid: number;
  status: 'launching' | 'running' | 'paused' | 'completed' | 'failed' | 'needs-input';
  repoPath: string;
  agentType: string;
  prompt?: string;
  createdAt: string;
  startedAt: string;
  updatedAt: string;
}

export interface Task {
  id: string;
  name: string;
  description: string;
  repoPath: string;
  agentType: string;
  status: 'pending' | 'running' | 'done' | 'failed' | 'needs-input';
  outputPath?: string;
  workflowId?: string;
  createdAt: string;
  startedAt: string;
  updatedAt: string;
}

export interface DashboardStats {
  total: number;
  running: number;
  pending: number;
  needsInput: number;
  done: number;
  failed: number;
}

export interface DashboardResponse {
  stats: DashboardStats;
  activeSessions: Session[];
  activeTasks: Task[];
}

export interface ActivityEvent {
  id: string;
  sessionId?: string;
  type: string;
  description: string;
  createdAt: string;
}

export interface Workspace {
  id: string;
  name: string;
  path: string;
  repoPaths: string[];
  prompt?: string;
  createdAt: string;
}

export interface ApprovalRequest {
  pid: number;
  toolName: string;
  args: Record<string, unknown>;
  riskLevel: 'low' | 'medium' | 'high';
  sessionId: string;
  createdAt: string;
}

export interface AppSettings {
  defaultAgent: string;
  scanIntervalSeconds: number;
  preferredTerminal: string;
  notificationsEnabled: boolean;
}

export interface SessionIndicator {
  pid: number;
  cwd: string;
  hasQuestion: boolean;
  status: string;
}

export interface Repo {
  name: string;
  path: string;
  language: string;
  branch: string;
  hasAgent: boolean;
  project?: string;
}

export interface RepoInfo {
  name: string;
  path: string;
  branch: string;
  remoteURL: string;
  lastCommitMessage: string;
  uncommittedFiles: number;
  isClean: boolean;
}

export interface DiffFile {
  path: string;
  insertions: number;
  deletions: number;
}

export interface DiffResult {
  files: DiffFile[];
  stats: {
    filesChanged: number;
    insertions: number;
    deletions: number;
  };
}

export interface JarvisChatResponse {
  response: string;
}

export interface LiveKitToken {
  url: string;
  token: string;
  room: string;
  identity: string;
}

// ---------------------------------------------------------------------------
// Google Calendar -- mirrors internal/model/gcal.go and the responses
// shipped by internal/api/handlers_calendar.go. Connected is the
// authoritative "is the Mac signed into Google?" flag so the mobile UI
// can render a connect-CTA when needed without inferring from null.
// ---------------------------------------------------------------------------

export interface CalendarEvent {
  id?: string;
  title?: string;
  start?: string;        // RFC3339
  end?: string;
  attendees?: string[];
  location?: string;
  htmlLink?: string;
  timeZone?: string;
}

export interface NextEventSnapshot {
  title?: string;
  start?: string;
  relativeTime?: string;  // server-formatted: "in 14m" / "now" / "in 2h"
  location?: string;
}

export interface NextCalendarResponse {
  connected: boolean;
  event: NextEventSnapshot | null;
}

export interface UpcomingCalendarResponse {
  connected: boolean;
  events: CalendarEvent[];
}

// ---------------------------------------------------------------------------
// Error type
// ---------------------------------------------------------------------------

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

// ---------------------------------------------------------------------------
// Internal fetch helpers
// ---------------------------------------------------------------------------

async function buildHeaders(): Promise<Record<string, string>> {
  const token = await storage.getToken();
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    Accept: 'application/json',
  };
  if (token) {
    headers['Authorization'] = `Bearer ${token}`;
  }
  return headers;
}

function isNetworkError(err: unknown): boolean {
  return err instanceof TypeError && (err as TypeError).message === 'Network request failed';
}

// One-shot logger -- only the first request per app launch logs the URL it
// resolved to. Removes the noise of 5-second poll spam while giving us a
// single diagnostic line we can use to confirm the pair record made it
// into ``getServerUrl()`` correctly.
let _diagFirstRequestLogged = false;

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
): Promise<T> {
  const serverUrl = await storage.getServerUrl();
  const url = `${serverUrl}${path}`;
  const headers = await buildHeaders();
  if (!_diagFirstRequestLogged) {
    _diagFirstRequestLogged = true;
    console.log('[api] first request resolved', {
      serverUrl,
      hasAuthHeader: Boolean(headers['Authorization']),
      authHeaderPrefix: headers['Authorization']
        ? headers['Authorization'].slice(0, 16) + '...'
        : '(missing)',
    });
  }

  const init: RequestInit = { method, headers };
  if (body !== undefined) {
    init.body = JSON.stringify(body);
  }

  let response: Response;

  try {
    response = await fetch(url, init);
  } catch (err: unknown) {
    // Auto-retry once on network error (not on HTTP 4xx/5xx)
    if (isNetworkError(err)) {
      try {
        response = await fetch(url, init);
      } catch (retryErr: unknown) {
        throw new ApiError(0, retryErr instanceof Error ? retryErr.message : 'Network request failed');
      }
    } else {
      throw new ApiError(0, err instanceof Error ? err.message : 'Unknown fetch error');
    }
  }

  if (!response.ok) {
    let message = response.statusText;
    try {
      const errorBody = (await response.json()) as { error?: string; message?: string };
      message = errorBody.error ?? errorBody.message ?? message;
    } catch {
      // body was not JSON, keep statusText
    }
    throw new ApiError(response.status, message);
  }

  // Handle 204 No Content or empty bodies
  const contentLength = response.headers.get('content-length');
  if (response.status === 204 || contentLength === '0') {
    return undefined as T;
  }

  const text = await response.text();
  if (text.length === 0) {
    return undefined as T;
  }

  return JSON.parse(text) as T;
}

// ---------------------------------------------------------------------------
// HTTP verb helpers
// ---------------------------------------------------------------------------

function get<T>(path: string): Promise<T> {
  return request<T>('GET', path);
}

function post<T>(path: string, body: unknown): Promise<T> {
  return request<T>('POST', path, body);
}

function put<T>(path: string, body: unknown): Promise<T> {
  return request<T>('PUT', path, body);
}

function del(path: string): Promise<void> {
  return request<void>('DELETE', path);
}

// ---------------------------------------------------------------------------
// Typed API surface
// ---------------------------------------------------------------------------

export const awmApi = {
  // Ping
  ping: (): Promise<{ ok: boolean }> => get<{ ok: boolean }>('/ping'),

  // Sessions
  listSessions: (status?: string): Promise<Session[]> =>
    get<Session[]>(`/sessions${status ? `?status=${status}` : ''}`),

  getSession: (id: string): Promise<Session> =>
    get<Session>(`/sessions/${id}`),

  stopSession: (id: string): Promise<void> =>
    post<void>(`/sessions/${id}/stop`, {}),

  // Dashboard
  getDashboard: (): Promise<DashboardResponse> =>
    get<DashboardResponse>('/dashboard'),

  getIndicators: (): Promise<SessionIndicator[]> =>
    get<SessionIndicator[]>('/indicators'),

  // Activity -- cursor-based pagination via before_id
  getActivity: (limit = 20, beforeId?: string): Promise<ActivityEvent[]> =>
    get<ActivityEvent[]>(
      `/activity?limit=${limit}${beforeId ? `&before_id=${beforeId}` : ''}`,
    ),

  // Workspaces
  listWorkspaces: (): Promise<Workspace[]> =>
    get<Workspace[]>('/workspaces'),

  deleteWorkspace: (id: string): Promise<void> =>
    del(`/workspaces/${id}`),

  // Approvals
  listApprovals: (): Promise<ApprovalRequest[]> =>
    get<ApprovalRequest[]>('/approvals'),

  respondToApproval: (pid: number, response: 'y' | 'n'): Promise<void> =>
    post<void>(`/approvals/${pid}/respond`, { response }),

  // Tasks
  getTask: (id: string): Promise<Task> =>
    get<Task>(`/tasks/${id}`),

  // Settings
  getSettings: (): Promise<AppSettings> =>
    get<AppSettings>('/settings'),

  updateSettings: (settings: Partial<AppSettings>): Promise<AppSettings> =>
    put<AppSettings>('/settings', settings),

  // Push notifications
  registerPushToken: (token: string): Promise<void> =>
    post<void>('/push-token', { token }),

  // Jarvis chat
  jarvisChat: (message: string): Promise<JarvisChatResponse> =>
    post<JarvisChatResponse>('/jarvis/chat', { message }),

  // Google Calendar -- read-only views. The Mac side owns the OAuth flow;
  // mobile only sees the pre-computed snapshots.
  getNextCalendarEvent: (): Promise<NextCalendarResponse> =>
    get<NextCalendarResponse>('/calendar/next'),

  getUpcomingCalendarEvents: (limit = 10): Promise<UpcomingCalendarResponse> =>
    get<UpcomingCalendarResponse>(`/calendar/upcoming?limit=${limit}`),

  // LiveKit token (spike — used by Voice screen to join the same room as the daemon)
  getLiveKitToken: (identity = 'phone'): Promise<LiveKitToken> =>
    get<LiveKitToken>(`/livekit/token?identity=${encodeURIComponent(identity)}`),

  // Terminal input
  sendToSession: (sessionId: string, command: string): Promise<void> =>
    post<void>(`/sessions/${sessionId}/send`, { command }),

  // Session launch
  launchSession: (agentType: string, repoPath: string, prompt: string): Promise<Session> =>
    post<Session>('/sessions', { agentType, repoPath, prompt }),

  // Repos
  listRepos: (): Promise<Repo[]> =>
    get<Repo[]>('/repos'),

  getRepoInfo: (name: string): Promise<RepoInfo> =>
    get<RepoInfo>(`/repos/${encodeURIComponent(name)}/info`),

  // Git
  getRepoDiff: (name: string): Promise<DiffResult> =>
    get<DiffResult>(`/repos/${encodeURIComponent(name)}/diff`),

  getStagedDiff: (name: string): Promise<DiffResult> =>
    get<DiffResult>(`/repos/${encodeURIComponent(name)}/staged`),

  gitStage: (name: string): Promise<void> =>
    post<void>(`/repos/${encodeURIComponent(name)}/git/stage`, {}),

  gitCommit: (name: string, message: string): Promise<void> =>
    post<void>(`/repos/${encodeURIComponent(name)}/git/commit`, { message }),

  gitPush: (name: string): Promise<void> =>
    post<void>(`/repos/${encodeURIComponent(name)}/git/push`, {}),
};
