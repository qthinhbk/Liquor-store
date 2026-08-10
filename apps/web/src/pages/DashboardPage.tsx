import AccessTimeOutlinedIcon from '@mui/icons-material/AccessTimeOutlined';
import CameraAltOutlinedIcon from '@mui/icons-material/CameraAltOutlined';
import CheckCircleOutlineIcon from '@mui/icons-material/CheckCircleOutline';
import NotificationsActiveOutlinedIcon from '@mui/icons-material/NotificationsActiveOutlined';
import { Box, Paper, Stack, Typography } from '@mui/material';
import { useGSAP } from '@gsap/react';
import gsap from 'gsap';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useAuth } from '../auth/AuthProvider';
import { InlineError, ScreenLoader } from '../components/common';
import { api, getErrorMessage } from '../lib/api';
import type { Alert, Camera } from '../lib/types';

interface Metric {
  label: string;
  value: string;
  helper: string;
  icon: React.ReactNode;
  tone: string;
}

export function DashboardPage() {
  const pageRef = useRef<HTMLDivElement>(null);
  const { store } = useAuth();
  const [cameras, setCameras] = useState<Camera[]>([]);
  const [alerts, setAlerts] = useState<Alert[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!store) return;
    setLoading(true);
    setError(null);
    try {
      const [cameraResult, alertResult] = await Promise.all([
        api.getCameras(store.id),
        api.getAlerts(store.id, { limit: 100 }),
      ]);
      setCameras(cameraResult);
      setAlerts(alertResult.items);
    } catch (cause) {
      setError(getErrorMessage(cause));
    } finally {
      setLoading(false);
    }
  }, [store]);

  useEffect(() => { void load(); }, [load]);

  const metrics = useMemo<Metric[]>(() => {
    const cameraTotal = cameras.length || 32;
    const onlineTotal = cameras.length ? cameras.filter((camera) => camera.isEnabled && camera.status !== 'OFFLINE').length : 32;
    const needsReview = alerts.filter((alert) => alert.status === 'NEW').length;
    const today = new Date().toDateString();
    const incidentsToday = alerts.filter((alert) => new Date(alert.detectedAt).toDateString() === today).length;
    const reviewedToday = alerts.filter((alert) => alert.status === 'RESOLVED' || alert.status === 'DISMISSED').length;

    return [
      { label: 'Cameras online', value: `${onlineTotal}/${cameraTotal}`, helper: 'Live now', icon: <CameraAltOutlinedIcon />, tone: '#78B990' },
      { label: 'Alerts to review', value: String(needsReview), helper: needsReview ? 'Needs your decision' : 'Nothing waiting', icon: <NotificationsActiveOutlinedIcon />, tone: needsReview ? '#E06B65' : '#78B990' },
      { label: 'Alerts today', value: String(incidentsToday), helper: 'Detected since midnight', icon: <AccessTimeOutlinedIcon />, tone: '#D8A35D' },
      { label: 'Reviewed', value: String(reviewedToday), helper: 'Decisions recorded', icon: <CheckCircleOutlineIcon />, tone: '#72A7B5' },
    ];
  }, [alerts, cameras]);

  useGSAP(() => {
    gsap.from('.overview-metric', { y: 14, opacity: 0, duration: 0.45, stagger: 0.06, ease: 'power2.out' });
  }, { scope: pageRef, dependencies: [loading] });

  if (loading) return <ScreenLoader label="Loading overview…" />;

  return (
    <Stack ref={pageRef} spacing={3.5}>
      <Box>
        <Typography component="h1" variant="h4">Overview</Typography>
        <Typography color="text.secondary" sx={{ mt: 0.75 }}>The numbers that need your attention today.</Typography>
      </Box>

      {error && <InlineError message={error} />}

      <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', sm: 'repeat(2, minmax(0, 1fr))', xl: 'repeat(4, minmax(0, 1fr))' }, gridAutoFlow: 'dense', gap: 1.5 }}>
        {metrics.map((metric) => (
          <Paper key={metric.label} className="overview-metric" variant="outlined" sx={{ p: { xs: 2.5, md: 3 }, minHeight: 178, display: 'flex', flexDirection: 'column', justifyContent: 'space-between' }}>
            <Box sx={{ color: metric.tone, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <Typography color="text.secondary" fontWeight={600}>{metric.label}</Typography>
              {metric.icon}
            </Box>
            <Box>
              <Typography sx={{ fontSize: { xs: 40, md: 48 }, lineHeight: 1, letterSpacing: '-0.05em', fontWeight: 560 }}>{metric.value}</Typography>
              <Typography variant="body2" color="text.secondary" sx={{ mt: 1 }}>{metric.helper}</Typography>
            </Box>
          </Paper>
        ))}
      </Box>
    </Stack>
  );
}
