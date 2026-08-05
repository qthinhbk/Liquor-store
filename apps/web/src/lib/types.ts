export type StoreRole = 'OWNER' | 'MANAGER' | 'OPERATOR';
export type CameraProtocol = 'RTSP' | 'ONVIF' | 'HLS' | 'WEBRTC';
export type CameraStatus = 'ONLINE' | 'OFFLINE' | 'DISABLED';
export type AlertStatus = 'NEW' | 'ACKNOWLEDGED' | 'DISMISSED' | 'RESOLVED';
export type AlertSeverity = 'LOW' | 'MEDIUM' | 'HIGH' | 'CRITICAL';
export type PersonCategory = 'EMPLOYEE' | 'CUSTOMER' | 'UNKNOWN';
export type ZoneKind = 'HIGH_VALUE' | 'CASHIER' | 'STOCKROOM' | 'ENTRANCE' | 'CUSTOM';

export interface StoreMembership {
  storeId: string;
  storeName: string;
  storeCode: string;
  role: StoreRole;
}

export interface CurrentUser {
  id: string;
  email: string;
  displayName: string;
  stores: StoreMembership[];
}

export interface AuthSession {
  accessToken: string;
  user: CurrentUser;
}

export interface Store {
  id: string;
  name: string;
  code: string;
  address: string | null;
  timezone: string;
  role: StoreRole;
}

export interface Camera {
  id: string;
  name: string;
  location: string;
  protocol: CameraProtocol;
  streamGatewayRef: string;
  status: CameraStatus;
  isEnabled: boolean;
  createdAt: string;
  updatedAt: string;
}

export type ZonePolygon = number[][];

export interface CameraZone {
  id: string;
  name: string;
  kind: ZoneKind;
  expectedPersonCategory: PersonCategory | null;
  polygon: ZonePolygon;
  dwellThresholdSeconds: number | null;
  isEnabled: boolean;
}

export interface Alert {
  id: string;
  sourceEventId: string;
  correlationId: string | null;
  type: string;
  severity: AlertSeverity;
  status: AlertStatus;
  subjectPersonCategory: PersonCategory;
  confidence: number | null;
  detectedAt: string;
  acknowledgedAt: string | null;
  resolutionNote: string | null;
  metadata: Record<string, unknown> | null;
  cameraId: string | null;
  cameraName: string | null;
  zoneId: string | null;
  zoneName: string | null;
}

export interface AlertEvidence {
  id: string;
  storageKey: string;
  mimeType: string;
  durationSeconds: number;
  startsAt: string;
  endsAt: string;
}

export interface AlertDetail extends Alert {
  acknowledgedById: string | null;
  acknowledgedByName: string | null;
  evidence: AlertEvidence[];
}

export interface AlertPage {
  items: Alert[];
  nextCursor: string | null;
}

export interface AlertFilters {
  status?: AlertStatus;
  severity?: AlertSeverity;
  type?: string;
  subjectPersonCategory?: PersonCategory;
  cameraId?: string;
  cursor?: string;
  limit?: number;
}

export interface CameraInput {
  name: string;
  location: string;
  protocol: CameraProtocol;
  streamGatewayRef: string;
  isEnabled: boolean;
}

export interface ZoneInput {
  name: string;
  kind: ZoneKind;
  expectedPersonCategory?: PersonCategory;
  polygon: ZonePolygon;
  dwellThresholdSeconds?: number;
}
