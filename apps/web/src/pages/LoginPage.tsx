import SecurityOutlinedIcon from '@mui/icons-material/SecurityOutlined';
import VisibilityOffOutlinedIcon from '@mui/icons-material/VisibilityOffOutlined';
import VisibilityOutlinedIcon from '@mui/icons-material/VisibilityOutlined';
import { Alert, Box, Button, IconButton, InputAdornment, Stack, TextField, Typography } from '@mui/material';
import { useState } from 'react';
import { Navigate, useLocation, useNavigate } from 'react-router-dom';
import { useAuth } from '../auth/AuthProvider';
import { getErrorMessage } from '../lib/api';

const allowedPostLoginPaths = new Set(['/dashboard', '/alerts', '/cameras']);

export function LoginPage() {
  const { user, login, isLoading } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [showPassword, setShowPassword] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  if (!isLoading && user) return <Navigate to="/dashboard" replace />;

  const handleSubmit = async (event: React.FormEvent) => {
    event.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      await login(email, password);
	  const requestedPath = (location.state as { from?: { pathname?: string } } | null)?.from?.pathname;
	  const destination = requestedPath && allowedPostLoginPaths.has(requestedPath) ? requestedPath : '/dashboard';
      navigate(destination, { replace: true });
    } catch (cause) {
      setError(getErrorMessage(cause));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Box sx={{ minHeight: '100vh', display: 'grid', gridTemplateColumns: { xs: '1fr', lg: 'minmax(0, 7fr) minmax(420px, 5fr)' }, bgcolor: '#090B0C', overflow: 'hidden' }}>
      <Box sx={{ minHeight: { xs: 360, lg: '100vh' }, position: 'relative', overflow: 'hidden', display: 'flex', flexDirection: 'column', justifyContent: 'space-between', p: { xs: 3, sm: 5, lg: 7 } }}>
        <Box component="img" src="/cctv/stockroom.png" alt="Monitored store interior" sx={{ position: 'absolute', inset: 0, width: '100%', height: '100%', objectFit: 'cover', filter: 'grayscale(1) contrast(1.24) brightness(.58)' }} />
        <Box sx={{ position: 'absolute', inset: 0, background: 'linear-gradient(135deg, rgba(9,11,12,.3), rgba(9,11,12,.88) 78%), radial-gradient(circle at 20% 15%, rgba(216,163,93,.2), transparent 38%)' }} />
        <Stack direction="row" spacing={1.5} alignItems="center" sx={{ position: 'relative' }}>
          <Box sx={{ display: 'grid', placeItems: 'center', width: 38, height: 38, borderRadius: '50%', border: '1px solid rgba(240,201,135,.5)', color: '#F0C987' }}><SecurityOutlinedIcon fontSize="small" /></Box>
          <Box><Typography color="#F4EFE6" fontWeight={650} lineHeight={1}>Nocturne</Typography><Typography sx={{ color: '#B1A996', fontSize: 9, letterSpacing: 1.8, mt: 0.5 }}>STORE INTELLIGENCE</Typography></Box>
        </Stack>
        <Box sx={{ position: 'relative', maxWidth: 900 }}>
          <Typography component="h1" sx={{ color: '#F4EFE6', fontSize: 'clamp(3rem, 6vw, 7rem)', lineHeight: 0.92, letterSpacing: '-0.065em', fontWeight: 500, maxWidth: 1040 }}>
            Clarity after closing time.
          </Typography>
          <Typography sx={{ color: '#C4BEB3', mt: 3, maxWidth: 500, lineHeight: 1.75 }}>
            A quiet command center for the incidents, cameras, and decisions that protect your store.
          </Typography>
        </Box>
      </Box>

      <Box sx={{ display: 'grid', placeItems: 'center', p: { xs: 3, sm: 6, lg: 8 }, position: 'relative' }}>
        <Box sx={{ width: '100%', maxWidth: 440 }}>
          <Typography sx={{ color: '#D8A35D', fontSize: 10, letterSpacing: 2, textTransform: 'uppercase', fontWeight: 700 }}>Secure access</Typography>
          <Typography variant="h4" sx={{ mt: 1.5, fontSize: { xs: 34, sm: 42 }, fontWeight: 520 }}>Welcome back.</Typography>
          <Typography color="text.secondary" sx={{ mt: 1.25, mb: 4, lineHeight: 1.65 }}>Sign in to review live operations and respond to security events.</Typography>
          {error && <Alert severity="error" sx={{ mb: 2.5 }}>{error}</Alert>}
          <Box component="form" onSubmit={handleSubmit}>
            <Stack spacing={2.25}>
              <TextField label="Email" type="email" autoComplete="email" value={email} onChange={(event) => setEmail(event.target.value)} required autoFocus fullWidth />
              <TextField label="Password" type={showPassword ? 'text' : 'password'} autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} required fullWidth
                InputProps={{ endAdornment: <InputAdornment position="end"><IconButton aria-label="toggle password visibility" edge="end" onClick={() => setShowPassword((value) => !value)}>{showPassword ? <VisibilityOffOutlinedIcon /> : <VisibilityOutlinedIcon />}</IconButton></InputAdornment> }} />
              <Button type="submit" variant="contained" size="large" disabled={submitting || isLoading} sx={{ mt: 1, py: 1.35 }}>{submitting ? 'Signing in…' : 'Enter command center'}</Button>
            </Stack>
          </Box>
          <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 4, lineHeight: 1.6 }}>Authorized store personnel only. Contact your system administrator if you need access.</Typography>
        </Box>
      </Box>
    </Box>
  );
}
