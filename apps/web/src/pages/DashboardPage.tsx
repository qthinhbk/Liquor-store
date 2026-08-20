import AccessTimeOutlinedIcon from '@mui/icons-material/AccessTimeOutlined';
import CameraAltOutlinedIcon from '@mui/icons-material/CameraAltOutlined';
import CheckCircleOutlineIcon from '@mui/icons-material/CheckCircleOutline';
import NotificationsActiveOutlinedIcon from '@mui/icons-material/NotificationsActiveOutlined';
import { Box, Paper, Stack, Typography } from '@mui/material';
import { useGSAP } from '@gsap/react';
import gsap from 'gsap';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link as RouterLink } from 'react-router-dom';
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
  to: string;
}

function hasVideoEvidence(alert: Alert) {
  return typeof alert.metadata?.videoUrl === 'string' && alert.metadata.videoUrl.length > 0;
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
    const videoAlerts = alerts.filter(hasVideoEvidence);
    const needsReview = videoAlerts.filter((alert) => alert.status === 'NEW').length;
    const today = new Date().toDateString();
    const incidentsToday = videoAlerts.filter((alert) => new Date(alert.detectedAt).toDateString() === today).length;
    const confirmedTotal = videoAlerts.filter((alert) => alert.status === 'RESOLVED' && alert.type !== 'WEAPON_DETECTED').length;

    return [
      { label: 'Cameras online', value: `${onlineTotal}/${cameraTotal}`, helper: 'View live cameras', icon: <CameraAltOutlinedIcon />, tone: '#78B990', to: '/cameras?status=LIVE' },
      { label: 'Alerts to review', value: String(needsReview), helper: needsReview ? 'Needs your decision' : 'Nothing waiting', icon: <NotificationsActiveOutlinedIcon />, tone: needsReview ? '#E06B65' : '#78B990', to: '/alerts?section=attention' },
      { label: 'Alerts today', value: String(incidentsToday), helper: 'View alerts since midnight', icon: <AccessTimeOutlinedIcon />, tone: '#D8A35D', to: '/alerts?period=today' },
      { label: 'Confirmed', value: String(confirmedTotal), helper: 'View confirmed incidents', icon: <CheckCircleOutlineIcon />, tone: '#72A7B5', to: '/alerts?section=confirmed' },
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
          <Paper
            key={metric.label}
            component={RouterLink}
            to={metric.to}
            className="overview-metric"
            variant="outlined"
            sx={{
              p: { xs: 2.5, md: 3 },
              minHeight: 178,
              display: 'flex',
              flexDirection: 'column',
              justifyContent: 'space-between',
              color: 'inherit',
              textDecoration: 'none',
              transition: 'border-color 180ms ease, transform 180ms ease, background-color 180ms ease',
              '&:hover': { borderColor: metric.tone, transform: 'translateY(-2px)', bgcolor: 'rgba(255,255,255,.018)' },
              '&:focus-visible': { outline: `2px solid ${metric.tone}`, outlineOffset: 3 },
            }}
          >
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
