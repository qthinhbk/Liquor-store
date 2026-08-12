import CheckCircleOutlineIcon from '@mui/icons-material/CheckCircleOutline';
import CloseIcon from '@mui/icons-material/Close';
import PlayCircleOutlineIcon from '@mui/icons-material/PlayCircleOutline';
import ReplayIcon from '@mui/icons-material/Replay';
import { Alert as MuiAlert, Box, Button, Chip, CircularProgress, Dialog, DialogContent, DialogTitle, IconButton, LinearProgress, Pagination, Paper, Stack, Typography } from '@mui/material';
import { useGSAP } from '@gsap/react';
import gsap from 'gsap';
import { useCallback, useEffect, useRef, useState } from 'react';
import { useAuth } from '../auth/AuthProvider';
import { EmptyState, InlineError, ScreenLoader, alertTitle, formatDateTime, toTitle } from '../components/common';
import { api, getErrorMessage } from '../lib/api';
import type { Alert, AlertDetail } from '../lib/types';

type ReviewDecision = 'match' | 'false-alarm';

const alertsPerPage = 5;

const contextImages = [
  '/cctv/counter.png',
  '/cctv/aisle.png',
  '/cctv/stockroom.png',
  '/cctv/entrance.png',
  '/cctv/backdoor.png',
  '/cctv/parking.png',
];

const alertDescriptions: Record<string, string> = {
  CASH_DRAWER_WITHOUT_CUSTOMER: 'The cash drawer was opened while no customer was visible at the counter.',
  SUSPICIOUS_CASH_HANDLING: 'Unusual cash handling was detected at the checkout counter.',
  POS_VOID_OR_REFUND: 'A void or refund happened at the register and may need a quick review.',
  UNAUTHORIZED_STOCKROOM_ACCESS: 'Someone entered the stockroom outside the expected access pattern.',
  HIGH_VALUE_ZONE_DWELL: 'A person stayed unusually long near high-value products.',
  WEAPON_DETECTED: 'The system detected an object that may be a weapon.',
  SUSPICIOUS_PRODUCT_CONCEALMENT: 'A person may have concealed a product from view.',
};

function imageForAlert(alert: Alert) {
  const value = alert.cameraId ?? alert.id;
  const seed = Array.from(value).reduce((total, character) => total + character.charCodeAt(0), 0);
  return contextImages[seed % contextImages.length];
}

function metadataUrl(alert: Alert, key: 'thumbnailUrl' | 'videoUrl' | 'trackingUrl') {
  const value = alert.metadata?.[key];
  return typeof value === 'string' ? value : null;
}

interface DetectionBoxValue {
  x: number;
  y: number;
  width: number;
  height: number;
}

interface TrackedBox extends DetectionBoxValue {
  trackId: number;
  label: string;
  confidence: number;
}

interface TrackFrame {
  time: number;
  boxes: TrackedBox[];
}

interface TrackingPayload {
  fps: number;
  model: string;
  tracker: string;
  frames: TrackFrame[];
}

function detectionForAlert(alert: Alert) {
  const value = alert.metadata?.detectionBox;
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
  const record = value as Record<string, unknown>;
  const box: DetectionBoxValue = {
    x: Number(record.x),
    y: Number(record.y),
    width: Number(record.width),
    height: Number(record.height),
  };
  if (Object.values(box).some((coordinate) => !Number.isFinite(coordinate) || coordinate < 0 || coordinate > 1)) return null;
  const label = typeof alert.metadata?.detectionLabel === 'string' ? alert.metadata.detectionLabel : 'Person';
  return { box, label };
}

function DetectionOverlay({ alert }: { alert: Alert }) {
  const detection = detectionForAlert(alert);
  if (!detection) return null;
  const { box, label } = detection;
  return (
    <Box
      sx={{
        position: 'absolute',
        left: `${box.x * 100}%`,
        top: `${box.y * 100}%`,
        width: `${box.width * 100}%`,
        height: `${box.height * 100}%`,
        border: '2px solid #FF5C53',
        boxShadow: '0 0 0 1px rgba(0,0,0,.55), inset 0 0 18px rgba(255,92,83,.08)',
        pointerEvents: 'none',
      }}
    >
      <Typography sx={{ position: 'absolute', left: -2, top: -2, bgcolor: '#FF5C53', color: '#160604', px: 0.75, py: 0.25, fontSize: 9, fontWeight: 850, letterSpacing: '.08em', textTransform: 'uppercase', whiteSpace: 'nowrap' }}>
        {label}
      </Typography>
    </Box>
  );
}

const trackColors = ['#FF5C53', '#F0C987', '#6BC5D2', '#9BD2AC', '#C19BFF'];

function TrackedBoxes({ boxes }: { boxes: TrackedBox[] }) {
  return boxes.map((box) => {
    const color = trackColors[Math.abs(box.trackId) % trackColors.length];
    return (
      <Box
        key={box.trackId}
        sx={{
          position: 'absolute',
          left: `${box.x * 100}%`,
          top: `${box.y * 100}%`,
          width: `${box.width * 100}%`,
          height: `${box.height * 100}%`,
          border: `2px solid ${color}`,
          boxShadow: '0 0 0 1px rgba(0,0,0,.58)',
          pointerEvents: 'none',
          transition: 'left 70ms linear, top 70ms linear, width 70ms linear, height 70ms linear',
        }}
      >
        <Typography sx={{ position: 'absolute', left: -2, top: -2, bgcolor: color, color: '#090B0C', px: 0.65, py: 0.2, fontSize: 8, fontWeight: 850, letterSpacing: '.05em', whiteSpace: 'nowrap' }}>
          {box.label} #{box.trackId} · {Math.round(box.confidence * 100)}%
        </Typography>
      </Box>
    );
  });
}

function TrackedAlertVideo({ alert, videoUrl, thumbnail }: { alert: Alert; videoUrl: string; thumbnail: string }) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const lastFrameRef = useRef(-1);
  const [tracking, setTracking] = useState<TrackingPayload | null>(null);
  const [boxes, setBoxes] = useState<TrackedBox[]>([]);
  const trackingUrl = metadataUrl(alert, 'trackingUrl');

  useEffect(() => {
    if (!trackingUrl) return;
    const controller = new AbortController();
    void fetch(trackingUrl, { signal: controller.signal })
      .then((response) => {
        if (!response.ok) throw new Error(`Tracking data returned ${response.status}.`);
        return response.json() as Promise<TrackingPayload>;
      })
      .then(setTracking)
      .catch((cause: unknown) => {
        if (cause instanceof DOMException && cause.name === 'AbortError') return;
        setTracking(null);
      });
    return () => controller.abort();
  }, [trackingUrl]);

  useEffect(() => {
    if (!tracking || !videoRef.current) return;
    let animationFrame = 0;
    const update = () => {
      const video = videoRef.current;
      if (video && tracking.frames.length > 0) {
        const frameIndex = Math.min(tracking.frames.length - 1, Math.max(0, Math.round(video.currentTime * tracking.fps)));
        if (frameIndex !== lastFrameRef.current) {
          lastFrameRef.current = frameIndex;
          setBoxes(tracking.frames[frameIndex]?.boxes ?? []);
        }
      }
      animationFrame = requestAnimationFrame(update);
    };
    animationFrame = requestAnimationFrame(update);
    return () => cancelAnimationFrame(animationFrame);
  }, [tracking]);

  return (
    <Box sx={{ aspectRatio: '16 / 9', bgcolor: '#050606', overflow: 'hidden', position: 'relative' }}>
      <Box ref={videoRef} component="video" src={videoUrl} poster={thumbnail} controls playsInline sx={{ width: '100%', height: '100%', objectFit: 'contain' }} />
      {tracking ? <TrackedBoxes boxes={boxes} /> : <DetectionOverlay alert={alert} />}
      {tracking && (
        <Typography sx={{ position: 'absolute', right: 10, top: 10, bgcolor: 'rgba(5,6,6,.78)', color: '#A8DAB7', px: 0.8, py: 0.4, fontSize: 9, fontWeight: 750, letterSpacing: '.08em', pointerEvents: 'none' }}>
          {tracking.model} · {tracking.tracker}
        </Typography>
      )}
    </Box>
  );
}

function hasVideo(alert: Alert) {
  return Boolean(metadataUrl(alert, 'videoUrl'));
}

function isEmergency(alert: Alert) {
  return alert.type === 'WEAPON_DETECTED';
}

function situationTitle(type: string) {
  const title = alertTitle(type).toLocaleLowerCase();
  return title.charAt(0).toLocaleUpperCase() + title.slice(1);
}

function sortActiveAlerts(items: Alert[]) {
  return [...items].sort((left, right) => {
    if (isEmergency(left) !== isEmergency(right)) return isEmergency(left) ? -1 : 1;
    return new Date(right.detectedAt).getTime() - new Date(left.detectedAt).getTime();
  });
}

function AlertPagination({ page, count, onChange }: { page: number; count: number; onChange: (page: number) => void }) {
  if (count <= 1) return null;
  return (
    <Box sx={{ mt: 2.25, display: 'flex', flexWrap: 'wrap', justifyContent: 'space-between', alignItems: 'center', gap: 1.5 }}>
      <Typography variant="caption" color="text.secondary">Page {page} of {count}</Typography>
      <Pagination
        page={page}
        count={count}
        onChange={(_, value) => onChange(value)}
        shape="rounded"
        color="primary"
        siblingCount={1}
        boundaryCount={1}
        size="small"
      />
    </Box>
  );
}

export function AlertsPage() {
  const pageRef = useRef<HTMLDivElement>(null);
  const attentionSectionRef = useRef<HTMLDivElement>(null);
  const confirmedSectionRef = useRef<HTMLDivElement>(null);
  const { store } = useAuth();
  const [alerts, setAlerts] = useState<Alert[]>([]);
  const [confirmedAlerts, setConfirmedAlerts] = useState<Alert[]>([]);
  const [attentionPage, setAttentionPage] = useState(1);
  const [confirmedPage, setConfirmedPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [reviewingId, setReviewingId] = useState<string | null>(null);
  const [detail, setDetail] = useState<AlertDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);

  const loadAlerts = useCallback(async () => {
    if (!store) return;
    setLoading(true);
    setError(null);
    try {
      const [activeResult, confirmedResult] = await Promise.all([
        api.getAlerts(store.id, { status: 'NEW', limit: 100 }),
        api.getAlerts(store.id, { status: 'RESOLVED', limit: 100 }),
      ]);
      setAlerts(sortActiveAlerts(activeResult.items.filter(hasVideo)));
      setConfirmedAlerts(confirmedResult.items.filter((alert) => hasVideo(alert) && !isEmergency(alert)));
    } catch (cause) {
      setError(getErrorMessage(cause));
    } finally {
      setLoading(false);
    }
  }, [store]);

  useEffect(() => { void loadAlerts(); }, [loadAlerts]);

  const attentionPageCount = Math.max(1, Math.ceil(alerts.length / alertsPerPage));
  const confirmedPageCount = Math.max(1, Math.ceil(confirmedAlerts.length / alertsPerPage));
  const safeAttentionPage = Math.min(attentionPage, attentionPageCount);
  const safeConfirmedPage = Math.min(confirmedPage, confirmedPageCount);
  const visibleAlerts = alerts.slice((safeAttentionPage - 1) * alertsPerPage, safeAttentionPage * alertsPerPage);
  const visibleConfirmedAlerts = confirmedAlerts.slice((safeConfirmedPage - 1) * alertsPerPage, safeConfirmedPage * alertsPerPage);

  useEffect(() => {
    setAttentionPage((current) => Math.min(current, attentionPageCount));
  }, [attentionPageCount]);

  useEffect(() => {
    setConfirmedPage((current) => Math.min(current, confirmedPageCount));
  }, [confirmedPageCount]);

  useGSAP(() => {
    gsap.from('.alert-review-card', { y: 12, opacity: 0, duration: 0.38, stagger: 0.045, ease: 'power2.out' });
  }, { scope: pageRef, dependencies: [loading] });

  const reviewAlert = async (alert: Alert, decision: ReviewDecision) => {
    if (!store || reviewingId || isEmergency(alert)) return;
    setReviewingId(alert.id);
    setError(null);
    try {
      if (decision === 'match') {
        await api.actionAlert(store.id, alert.id, 'resolve', 'Owner review: confirmed as a real incident.');
        setConfirmedAlerts((current) => [{
          ...alert,
          status: 'RESOLVED',
          acknowledgedAt: new Date().toISOString(),
          resolutionNote: 'Owner review: confirmed as a real incident.',
        }, ...current.filter((item) => item.id !== alert.id)]);
      } else {
        await api.actionAlert(store.id, alert.id, 'dismiss', 'Owner review: marked as a false alarm.');
      }
      setAlerts((current) => current.filter((item) => item.id !== alert.id));
      if (detail?.id === alert.id) setDetail(null);
    } catch (cause) {
      setError(getErrorMessage(cause));
    } finally {
      setReviewingId(null);
    }
  };

  const openContext = async (alert: Alert) => {
    if (!store) return;
    setDetailLoading(true);
    setDetail({ ...alert, acknowledgedById: null, acknowledgedByName: null, evidence: [] });
    try {
      setDetail(await api.getAlert(store.id, alert.id));
    } catch (cause) {
      setError(getErrorMessage(cause));
    } finally {
      setDetailLoading(false);
    }
  };

  if (loading) return <ScreenLoader label="Loading alerts…" />;

  return (
    <Stack ref={pageRef} spacing={3.5}>
      <Box sx={{ display: 'flex', flexWrap: 'wrap', justifyContent: 'space-between', alignItems: 'end', gap: 2 }}>
        <Box>
          <Typography component="h1" variant="h4">Alerts</Typography>
          <Typography color="text.secondary" sx={{ mt: 0.75, maxWidth: 700 }}>
            Emergency alerts require immediate review. Suspicious behavior alerts can be confirmed or dismissed as false alarms.
          </Typography>
        </Box>
        <Button variant="outlined" startIcon={<ReplayIcon />} onClick={() => void loadAlerts()}>Refresh</Button>
      </Box>

      {error && <InlineError message={error} />}

      <Paper variant="outlined" sx={{ px: 2.5, py: 2, display: 'flex', alignItems: 'center', gap: 1.5, borderColor: alerts.length ? '#5A472E' : '#2C3931', bgcolor: alerts.length ? 'rgba(216,163,93,.06)' : 'rgba(120,185,144,.05)' }}>
        <Typography sx={{ fontSize: 30, lineHeight: 1, fontWeight: 650, color: alerts.length ? '#F0C987' : '#9BD2AC' }}>{alerts.length}</Typography>
        <Box>
          <Typography fontWeight={650}>{alerts.length === 1 ? 'active alert' : 'active alerts'}</Typography>
          <Typography variant="body2" color="text.secondary">Emergency alerts appear first. Confirmed incidents move to the archive below.</Typography>
        </Box>
      </Paper>

      <Box ref={attentionSectionRef} sx={{ scrollMarginTop: 96 }}>
        <Typography variant="h5">Needs attention</Typography>
        <Typography variant="body2" color="text.secondary" sx={{ mt: 0.5, mb: 2 }}>Only alerts with recorded video are shown.</Typography>
      {alerts.length === 0 ? (
        <EmptyState title="You are all caught up" description="There are no new video alerts to review." />
      ) : (
        <Stack spacing={1.5}>
          {visibleAlerts.map((alert) => {
            const isSaving = reviewingId === alert.id;
            const thumbnail = metadataUrl(alert, 'thumbnailUrl') ?? imageForAlert(alert);
            return (
              <Paper key={alert.id} className="alert-review-card" variant="outlined" sx={{ overflow: 'hidden', position: 'relative' }}>
                {isSaving && <LinearProgress sx={{ position: 'absolute', zIndex: 2, top: 0, left: 0, right: 0 }} />}
                <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: '260px minmax(0, 1fr)', xl: '280px minmax(0, 1fr) 250px' }, minHeight: 210 }}>
                  <Box
                    component="button"
                    type="button"
                    aria-label={`Play video for ${situationTitle(alert.type)}`}
                    onClick={() => void openContext(alert)}
                    sx={{
                      position: 'relative',
                      minHeight: { xs: 210, md: '100%' },
                      overflow: 'hidden',
                      bgcolor: '#050606',
                      display: 'grid',
                      placeItems: 'center',
                      width: '100%',
                      p: 0,
                      border: 0,
                      color: 'inherit',
                      cursor: 'pointer',
                      '& .alert-thumbnail': { transition: 'transform 280ms ease, filter 280ms ease' },
                      '& .play-indicator': { opacity: 0, transform: 'scale(.92)', transition: 'opacity 180ms ease, transform 180ms ease' },
                      '&:hover .alert-thumbnail': { transform: 'scale(1.018)', filter: 'brightness(.62) contrast(1.14)' },
                      '&:hover .play-indicator, &:focus-visible .play-indicator': { opacity: 1, transform: 'scale(1)' },
                      '&:focus-visible': { outline: '2px solid #D8A35D', outlineOffset: -2 },
                    }}
                  >
                    <Box sx={{ position: 'relative', width: '100%', aspectRatio: '16 / 9', overflow: 'hidden' }}>
                      <Box className="alert-thumbnail" component="img" src={thumbnail} alt={`Video preview for ${situationTitle(alert.type)}`} sx={{ width: '100%', height: '100%', position: 'absolute', inset: 0, objectFit: 'cover', filter: 'brightness(.72) contrast(1.12)' }} />
                      <DetectionOverlay alert={alert} />
                    </Box>
                    <Box className="play-indicator" aria-hidden="true" sx={{ position: 'absolute', inset: 0, display: 'grid', placeItems: 'center', pointerEvents: 'none' }}>
                      <PlayCircleOutlineIcon sx={{ color: '#FFF', fontSize: 45, filter: 'drop-shadow(0 2px 10px rgba(0,0,0,.72))' }} />
                    </Box>
                  </Box>

                  <Box sx={{ p: { xs: 2.25, md: 3 }, minWidth: 0 }}>
                    <Stack direction="row" spacing={1} alignItems="center" useFlexGap flexWrap="wrap">
                      <Chip
                        size="small"
                        variant="outlined"
                        label={isEmergency(alert) ? 'Emergency alert' : 'Suspicious behavior'}
                        sx={{ color: isEmergency(alert) ? '#FF817A' : '#F0C987', borderColor: isEmergency(alert) ? '#FF6B63' : '#D8A35D' }}
                      />
                      <Typography variant="body2" color="text.secondary">{formatDateTime(alert.detectedAt)}</Typography>
                    </Stack>
                    <Typography variant="h5" sx={{ mt: 1.5 }}>{situationTitle(alert.type)}</Typography>
                    <Typography color="text.secondary" sx={{ mt: 1, lineHeight: 1.65 }}>{alertDescriptions[alert.type] ?? 'The security system detected activity that needs a quick review.'}</Typography>
                    <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 2.5, mt: 2 }}>
                      <ContextValue label="Camera" value={alert.cameraName ?? 'Unknown camera'} />
                      <ContextValue label="Person" value={toTitle(alert.subjectPersonCategory)} />
                      {alert.confidence !== null && <ContextValue label="AI confidence" value={`${Math.round(alert.confidence * 100)}%`} />}
                    </Box>
                  </Box>

                  <Box sx={{ gridColumn: { md: '1 / -1', xl: 'auto' }, borderLeft: { xl: '1px solid #252A29' }, borderTop: { xs: '1px solid #252A29', xl: 0 }, p: 2.25, display: 'flex', flexDirection: 'column', justifyContent: 'center', gap: 1 }}>
                    {isEmergency(alert) ? (
                      <>
                        <Typography fontWeight={700} color="#FF817A">Immediate review required</Typography>
                        <Typography variant="body2" color="text.secondary" sx={{ lineHeight: 1.65 }}>Review the footage and follow the store emergency procedure. Emergency alerts do not have Match or False alarm options.</Typography>
                      </>
                    ) : (
                      <>
                        <Typography fontWeight={650} sx={{ mb: 0.5 }}>What did you find?</Typography>
                        <Button fullWidth variant="contained" color="success" startIcon={<CheckCircleOutlineIcon />} disabled={Boolean(reviewingId)} onClick={() => void reviewAlert(alert, 'match')} sx={{ justifyContent: 'flex-start', py: 1.2 }}>
                          Match · Real incident
                        </Button>
                        <Button fullWidth variant="outlined" startIcon={<CloseIcon />} disabled={Boolean(reviewingId)} onClick={() => void reviewAlert(alert, 'false-alarm')} sx={{ justifyContent: 'flex-start', py: 1.2 }}>
                          False alarm
                        </Button>
                        <Typography variant="caption" color="text.secondary" sx={{ mt: 0.5, lineHeight: 1.5 }}>Confirmed incidents move to the archive below.</Typography>
                      </>
                    )}
                  </Box>
                </Box>
              </Paper>
            );
          })}
          <AlertPagination
            page={safeAttentionPage}
            count={attentionPageCount}
            onChange={(page) => {
              setAttentionPage(page);
              attentionSectionRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' });
            }}
          />
        </Stack>
      )}
      </Box>

      <Box ref={confirmedSectionRef} sx={{ pt: 1, scrollMarginTop: 96 }}>
        <Stack direction="row" justifyContent="space-between" alignItems="end" gap={2} sx={{ mb: 2 }}>
          <Box>
            <Typography variant="h5">Confirmed alerts</Typography>
            <Typography variant="body2" color="text.secondary" sx={{ mt: 0.5 }}>Real incidents confirmed by the owner, retained for later review.</Typography>
          </Box>
          <Chip size="small" color="success" variant="outlined" label={`${confirmedAlerts.length} confirmed`} />
        </Stack>
        {confirmedAlerts.length === 0 ? (
          <EmptyState title="No confirmed alerts yet" description="An alert confirmed as a real incident will appear here." />
        ) : (
          <Stack spacing={1.5}>
            {visibleConfirmedAlerts.map((alert) => (
              <ConfirmedAlertCard key={alert.id} alert={alert} onOpen={openContext} />
            ))}
            <AlertPagination
              page={safeConfirmedPage}
              count={confirmedPageCount}
              onChange={(page) => {
                setConfirmedPage(page);
                confirmedSectionRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' });
              }}
            />
          </Stack>
        )}
      </Box>

      <Dialog open={Boolean(detail)} onClose={() => !detailLoading && setDetail(null)} fullWidth maxWidth="md">
        <DialogTitle sx={{ pr: 7 }}>
          {detail ? (isEmergency(detail) ? 'Emergency alert' : 'Suspicious behavior alert') : 'Alert context'}
          <IconButton aria-label="Close" onClick={() => setDetail(null)} sx={{ position: 'absolute', right: 12, top: 10 }}><CloseIcon /></IconButton>
        </DialogTitle>
        <DialogContent>
          {detailLoading && <Box sx={{ display: 'grid', placeItems: 'center', minHeight: 260 }}><CircularProgress /></Box>}
          {!detailLoading && detail && (
            <AlertContext
              alert={detail}
              reviewing={reviewingId === detail.id}
              onReview={(decision) => reviewAlert(detail, decision)}
            />
          )}
        </DialogContent>
      </Dialog>
    </Stack>
  );
}

function ConfirmedAlertCard({ alert, onOpen }: { alert: Alert; onOpen: (alert: Alert) => void | Promise<void> }) {
  const thumbnail = metadataUrl(alert, 'thumbnailUrl') ?? imageForAlert(alert);
  return (
    <Paper className="alert-review-card" variant="outlined" sx={{ overflow: 'hidden', borderColor: '#2C4937' }}>
      <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: '260px minmax(0, 1fr)' }, minHeight: 190 }}>
        <Box
          component="button"
          type="button"
          aria-label={`Play video for ${situationTitle(alert.type)}`}
          onClick={() => void onOpen(alert)}
          sx={{
            position: 'relative',
            minHeight: { xs: 210, md: '100%' },
            overflow: 'hidden',
            bgcolor: '#050606',
            display: 'grid',
            placeItems: 'center',
            width: '100%',
            p: 0,
            border: 0,
            color: 'inherit',
            cursor: 'pointer',
            '& .alert-thumbnail': { transition: 'transform 280ms ease, filter 280ms ease' },
            '& .play-indicator': { opacity: 0, transform: 'scale(.92)', transition: 'opacity 180ms ease, transform 180ms ease' },
            '&:hover .alert-thumbnail': { transform: 'scale(1.018)', filter: 'brightness(.58) contrast(1.1)' },
            '&:hover .play-indicator, &:focus-visible .play-indicator': { opacity: 1, transform: 'scale(1)' },
            '&:focus-visible': { outline: '2px solid #78B990', outlineOffset: -2 },
          }}
        >
          <Box sx={{ position: 'relative', width: '100%', aspectRatio: '16 / 9', overflow: 'hidden' }}>
            <Box className="alert-thumbnail" component="img" src={thumbnail} alt={`Video preview for ${situationTitle(alert.type)}`} sx={{ width: '100%', height: '100%', position: 'absolute', inset: 0, objectFit: 'cover', filter: 'brightness(.66) contrast(1.08)' }} />
            <DetectionOverlay alert={alert} />
          </Box>
          <Box className="play-indicator" aria-hidden="true" sx={{ position: 'absolute', inset: 0, display: 'grid', placeItems: 'center', pointerEvents: 'none' }}>
            <PlayCircleOutlineIcon sx={{ color: '#FFF', fontSize: 45, filter: 'drop-shadow(0 2px 10px rgba(0,0,0,.72))' }} />
          </Box>
        </Box>
        <Box sx={{ p: { xs: 2.25, md: 3 }, minWidth: 0 }}>
          <Stack direction="row" spacing={1} alignItems="center" useFlexGap flexWrap="wrap">
            <Chip size="small" variant="outlined" label="Suspicious behavior" sx={{ color: '#F0C987', borderColor: '#D8A35D' }} />
            <Chip size="small" color="success" label="Confirmed" />
            <Typography variant="body2" color="text.secondary">{formatDateTime(alert.detectedAt)}</Typography>
          </Stack>
          <Typography variant="h6" sx={{ mt: 1.5 }}>{situationTitle(alert.type)}</Typography>
          <Typography color="text.secondary" sx={{ mt: 0.75, lineHeight: 1.65 }}>{alertDescriptions[alert.type] ?? 'Confirmed security incident.'}</Typography>
          <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 2.5, mt: 2 }}>
            <ContextValue label="Camera" value={alert.cameraName ?? 'Unknown camera'} />
            <ContextValue label="Person" value={toTitle(alert.subjectPersonCategory)} />
            <ContextValue label="Decision" value="Matched · Real incident" />
          </Box>
        </Box>
      </Box>
    </Paper>
  );
}

function ContextValue({ label, value }: { label: string; value: string }) {
  return <Box><Typography variant="caption" color="text.secondary">{label}</Typography><Typography variant="body2" fontWeight={650} sx={{ mt: 0.2 }}>{value}</Typography></Box>;
}

function AlertContext({ alert, reviewing, onReview }: { alert: AlertDetail; reviewing: boolean; onReview: (decision: ReviewDecision) => void | Promise<void> }) {
  const videoUrl = metadataUrl(alert, 'videoUrl');
  const thumbnail = metadataUrl(alert, 'thumbnailUrl') ?? imageForAlert(alert);

  return (
    <Stack spacing={2.5}>
      {videoUrl ? (
        <TrackedAlertVideo alert={alert} videoUrl={videoUrl} thumbnail={thumbnail} />
      ) : (
        <Box sx={{ aspectRatio: '16 / 9', bgcolor: '#050606', overflow: 'hidden', position: 'relative' }}>
          <Box component="img" src={thumbnail} alt="Alert context" sx={{ width: '100%', height: '100%', objectFit: 'cover', filter: 'brightness(.78) contrast(1.1)' }} />
          <DetectionOverlay alert={alert} />
        </Box>
      )}
      {!videoUrl && <MuiAlert severity="info">A video link has not been attached to this alert yet. The available camera snapshot is shown instead.</MuiAlert>}
      <Typography color="text.secondary" sx={{ lineHeight: 1.7 }}>{alertDescriptions[alert.type] ?? 'Review the recorded context before making a decision.'}</Typography>
      <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr 1fr', sm: isEmergency(alert) ? '1fr 1fr' : 'minmax(150px, .8fr) minmax(130px, .65fr) minmax(310px, 1.55fr)' }, gap: 2, alignItems: 'end' }}>
        <ContextValue label="Detected" value={formatDateTime(alert.detectedAt)} />
        <ContextValue label="Camera" value={alert.cameraName ?? 'Unknown'} />
        {!isEmergency(alert) && (
          <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1} sx={{ gridColumn: { xs: '1 / -1', sm: 'auto' }, justifySelf: { sm: 'end' }, width: { xs: '100%', sm: 'auto' } }}>
            <Button
              variant="contained"
              color="success"
              startIcon={<CheckCircleOutlineIcon />}
              disabled={reviewing}
              onClick={() => void onReview('match')}
              sx={{ minWidth: 160 }}
            >
              Match · Real incident
            </Button>
            <Button
              variant="outlined"
              startIcon={<CloseIcon />}
              disabled={reviewing}
              onClick={() => void onReview('false-alarm')}
              sx={{ minWidth: 132 }}
            >
              False alarm
            </Button>
          </Stack>
        )}
      </Box>
    </Stack>
  );
}
