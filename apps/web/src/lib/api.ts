import type { Alert, AlertDetail, AlertFilters, AlertPage, AuthSession, Camera, CameraInput, CameraZone, Store, ZoneInput } from './types';

const baseUrl = (import.meta.env.VITE_API_BASE_URL ?? '/api/v1').replace(/\/$/, '');

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

class ApiClient {
  private accessToken: string | null = null;
  private refreshPromise: Promise<AuthSession> | null = null;

  async login(email: string, password: string) {
    const session = await this.rawRequest<AuthSession>('/auth/login', {
      method: 'POST',
      body: { email, password },
    });
    this.accessToken = session.accessToken;
    return session;
  }

  async refresh() {
    if (!this.refreshPromise) {
      this.refreshPromise = this.rawRequest<AuthSession>('/auth/refresh', { method: 'POST' })
        .then((session) => {
          this.accessToken = session.accessToken;
          return session;
        })
        .finally(() => {
          this.refreshPromise = null;
        });
    }
    return this.refreshPromise;
  }

  async logout() {
    try {
      await this.rawRequest<{ success: boolean }>('/auth/logout', { method: 'POST' });
    } finally {
      this.accessToken = null;
    }
  }

  async getStores() {
    return this.request<Store[]>('/stores');
  }

  async getCameras(storeId: string) {
    return this.request<Camera[]>(`/stores/${storeId}/cameras`);
  }

  async createCamera(storeId: string, input: CameraInput) {
    return this.request<Camera>(`/stores/${storeId}/cameras`, { method: 'POST', body: input });
  }

  async updateCamera(storeId: string, cameraId: string, input: Partial<CameraInput>) {
    return this.request<Camera>(`/stores/${storeId}/cameras/${cameraId}`, { method: 'PATCH', body: input });
  }

  async deleteCamera(storeId: string, cameraId: string) {
    return this.request<{ success: boolean }>(`/stores/${storeId}/cameras/${cameraId}`, { method: 'DELETE' });
  }

  async getZones(storeId: string, cameraId: string) {
    return this.request<CameraZone[]>(`/stores/${storeId}/cameras/${cameraId}/zones`);
  }

  async createZone(storeId: string, cameraId: string, input: ZoneInput) {
    return this.request<CameraZone>(`/stores/${storeId}/cameras/${cameraId}/zones`, { method: 'POST', body: input });
  }

  async updateZone(storeId: string, cameraId: string, zoneId: string, input: Partial<ZoneInput>) {
    return this.request<CameraZone>(`/stores/${storeId}/cameras/${cameraId}/zones/${zoneId}`, { method: 'PATCH', body: input });
  }

  async deleteZone(storeId: string, cameraId: string, zoneId: string) {
    return this.request<{ success: boolean }>(`/stores/${storeId}/cameras/${cameraId}/zones/${zoneId}`, { method: 'DELETE' });
  }

  async getAlerts(storeId: string, filters: AlertFilters = {}) {
    const query = new URLSearchParams();
    for (const [key, value] of Object.entries(filters)) {
      if (value !== undefined && value !== '') query.set(key, String(value));
    }
    const suffix = query.size ? `?${query.toString()}` : '';
    return this.request<AlertPage>(`/stores/${storeId}/alerts${suffix}`);
  }

  async getAlert(storeId: string, alertId: string) {
    return this.request<AlertDetail>(`/stores/${storeId}/alerts/${alertId}`);
  }

  async actionAlert(storeId: string, alertId: string, action: 'acknowledge' | 'dismiss' | 'resolve', note?: string) {
    return this.request<Alert>(`/stores/${storeId}/alerts/${alertId}/${action}`, {
      method: 'POST',
      body: note?.trim() ? { note: note.trim() } : {},
    });
  }

  private async request<T>(path: string, options: RequestOptions = {}, retry = true): Promise<T> {
    try {
      return await this.rawRequest<T>(path, options, true);
    } catch (error) {
      if (retry && error instanceof ApiError && error.status === 401) {
        await this.refresh();
        return this.request<T>(path, options, false);
      }
      throw error;
    }
  }

  private async rawRequest<T>(path: string, options: RequestOptions = {}, includeAccessToken = false): Promise<T> {
    const headers = new Headers(options.headers);
    if (options.body !== undefined) headers.set('Content-Type', 'application/json');
    if (includeAccessToken && this.accessToken) headers.set('Authorization', `Bearer ${this.accessToken}`);

    let response: Response;
    try {
      response = await fetch(`${baseUrl}${path}`, {
        method: options.method ?? 'GET',
        headers,
        body: options.body === undefined ? undefined : JSON.stringify(options.body),
        credentials: 'include',
      });
    } catch {
      throw new ApiError('Cannot reach the API. Check that the backend is running.', 0);
    }

    const payload = await response.json().catch(() => null) as { message?: string | string[] } | T | null;
    if (!response.ok) {
      const message = typeof payload === 'object' && payload && 'message' in payload
        ? Array.isArray(payload.message) ? payload.message.join(', ') : payload.message ?? 'The request failed.'
        : 'The request failed.';
      throw new ApiError(message, response.status);
    }
    return payload as T;
  }
}

interface RequestOptions {
  method?: 'GET' | 'POST' | 'PATCH' | 'DELETE';
  headers?: HeadersInit;
  body?: unknown;
}

export const api = new ApiClient();

export function getErrorMessage(error: unknown) {
  return error instanceof Error ? error.message : 'Something went wrong. Please try again.';
}
