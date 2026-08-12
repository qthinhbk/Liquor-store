import CameraAltOutlinedIcon from '@mui/icons-material/CameraAltOutlined';
import ErrorOutlineIcon from '@mui/icons-material/ErrorOutline';
import VideocamOffOutlinedIcon from '@mui/icons-material/VideocamOffOutlined';
import { Alert, Box, Chip, CircularProgress, Paper, Stack, Typography } from '@mui/material';
import type { AlertStatus, CameraStatus } from '../lib/types';

export function ScreenLoader({ label = 'Loading dashboard…' }: { label?: string }) {
  return (
    <Stack alignItems="center" justifyContent="center" spacing={2} sx={{ minHeight: '50vh' }}>
      <CircularProgress />
      <Typography color="text.secondary">{label}</Typography>
    </Stack>
  );
}

export function InlineError({ message }: { message: string }) {
  return <Alert severity="error" icon={<ErrorOutlineIcon />}>{message}</Alert>;
}

export function EmptyState({
  title,
  description,
  icon = <VideocamOffOutlinedIcon sx={{ fontSize: 42 }} />,
}: {
  title: string;
  description: string;
  icon?: React.ReactNode;
}) {
  return (
    <Paper variant="outlined" sx={{ py: 7, px: 3, textAlign: 'center', borderStyle: 'dashed' }}>
      <Box sx={{ color: 'text.secondary', mb: 1 }}>{icon}</Box>
      <Typography variant="h6">{title}</Typography>
      <Typography color="text.secondary" sx={{ mt: 0.75, maxWidth: 460, mx: 'auto' }}>{description}</Typography>
    </Paper>
  );
}

export function CameraStatusChip({ status, enabled = true }: { status: CameraStatus; enabled?: boolean }) {
  const label = !enabled ? 'Disabled' : status.charAt(0) + status.slice(1).toLowerCase();
  const color = !enabled || status === 'DISABLED' ? 'default' : status === 'ONLINE' ? 'success' : 'warning';
  return <Chip size="small" color={color} label={label} />;
}

export function AlertStatusChip({ status }: { status: AlertStatus }) {
  const colors: Record<AlertStatus, 'error' | 'info' | 'default' | 'success'> = {
    NEW: 'error',
    ACKNOWLEDGED: 'info',
    DISMISSED: 'default',
    RESOLVED: 'success',
  };
  return <Chip size="small" color={colors[status]} label={toTitle(status)} />;
}

export function formatDateTime(value: string | null | undefined) {
  if (!value) return '—';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '—' : new Intl.DateTimeFormat('en-US', {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(date);
}

export function alertTitle(type: string) {
  return type.split('_').map((part) => part.charAt(0) + part.slice(1).toLowerCase()).join(' ');
}

export function toTitle(value: string) {
  return value.split('_').map((part) => part.charAt(0) + part.slice(1).toLowerCase()).join(' ');
}

export function CameraEmptyState() {
  return <EmptyState title="No cameras found" description="Add a camera to begin configuring its secure stream reference and detection zones." icon={<CameraAltOutlinedIcon sx={{ fontSize: 42 }} />} />;
}
