import CameraAltOutlinedIcon from '@mui/icons-material/CameraAltOutlined';
import CheckCircleOutlineIcon from '@mui/icons-material/CheckCircleOutline';
import GridViewOutlinedIcon from '@mui/icons-material/GridViewOutlined';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import LiquorOutlinedIcon from '@mui/icons-material/LiquorOutlined';
import LogoutOutlinedIcon from '@mui/icons-material/LogoutOutlined';
import MenuIcon from '@mui/icons-material/Menu';
import NotificationsActiveOutlinedIcon from '@mui/icons-material/NotificationsActiveOutlined';
import RadioButtonUncheckedIcon from '@mui/icons-material/RadioButtonUnchecked';
import { AppBar, Avatar, Box, Collapse, Divider, Drawer, IconButton, List, ListItemButton, ListItemIcon, ListItemText, Toolbar, Tooltip, Typography, useMediaQuery } from '@mui/material';
import { useTheme } from '@mui/material/styles';
import { useQuery } from '@tanstack/react-query';
import { useEffect, useState } from 'react';
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom';
import { useAuth } from '../auth/AuthProvider';
import { api } from '../lib/api';
import type { Alert } from '../lib/types';

const drawerWidth = 232;

const navigation = [
  { label: 'Overview', to: '/dashboard', icon: <GridViewOutlinedIcon fontSize="small" /> },
  { label: 'Alert', to: '/alerts', icon: <NotificationsActiveOutlinedIcon fontSize="small" /> },
  { label: 'Camera', to: '/cameras', icon: <CameraAltOutlinedIcon fontSize="small" /> },
];

function hasVideoEvidence(alert: Alert) {
  return typeof alert.metadata?.videoUrl === 'string' && alert.metadata.videoUrl.length > 0;
}

export function AppShell() {
  const theme = useTheme();
  const isDesktop = useMediaQuery(theme.breakpoints.up('md'));
  const [isMobileOpen, setIsMobileOpen] = useState(false);
  const { user, store, logout } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const currentPage = navigation.find((item) => location.pathname.startsWith(item.to))?.label ?? 'Overview';
  const isAlertPage = location.pathname.startsWith('/alerts');
  const alertSection = new URLSearchParams(location.search).get('section');

  const { data: alertNavCounts = { attention: 0, confirmed: 0 }, refetch: refetchAlertNavCounts } = useQuery({
    queryKey: ['alert-navigation-counts', store?.id],
    queryFn: async () => {
      const [attentionResult, confirmedResult] = await Promise.all([
        api.getAlerts(store!.id, { status: 'NEW', limit: 100 }),
        api.getAlerts(store!.id, { status: 'RESOLVED', limit: 100 }),
      ]);
      return {
        attention: attentionResult.items.filter(hasVideoEvidence).length,
        confirmed: confirmedResult.items.filter((alert) => hasVideoEvidence(alert) && alert.type !== 'WEAPON_DETECTED').length,
      };
    },
    enabled: Boolean(store),
    staleTime: 15_000,
    refetchInterval: 30_000,
  });

  useEffect(() => {
    const handleAlertsChanged = () => { void refetchAlertNavCounts(); };
    window.addEventListener('alerts:changed', handleAlertsChanged);
    return () => window.removeEventListener('alerts:changed', handleAlertsChanged);
  }, [refetchAlertNavCounts]);

  const handleLogout = async () => {
    await logout();
    navigate('/login', { replace: true });
  };

  const drawer = (
    <Box
      component="aside"
      sx={{
        height: '100%',
        display: 'flex',
        flexDirection: 'column',
        bgcolor: '#0B0E0F',
        color: '#989C99',
      }}
    >
      <Box
        component={NavLink}
        to="/dashboard"
        onClick={() => setIsMobileOpen(false)}
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: 1.25,
          mx: 2.5,
          mt: 2.5,
          mb: 3.5,
          minWidth: 0,
          color: 'inherit',
          textDecoration: 'none',
          borderRadius: 0.75,
          transition: 'opacity 180ms ease',
          '&:hover': { opacity: 0.78 },
          '&:focus-visible': { outline: '2px solid #D8A35D', outlineOffset: 4 },
        }}
      >
        <LiquorOutlinedIcon aria-hidden="true" sx={{ flexShrink: 0, color: '#B99665', fontSize: 24 }} />
        <Box minWidth={0}>
          <Typography
            color="#F4EFE6"
            noWrap
            sx={{
              fontSize: 13,
              fontWeight: 650,
              letterSpacing: '0.085em',
              lineHeight: 1.2,
            }}
          >
            LIQUOR STORE
          </Typography>
          <Typography
            variant="caption"
            color="#7F8582"
            noWrap
            display="block"
            sx={{ mt: 0.55, maxWidth: '100%', fontSize: 11.25, lineHeight: 1.25, letterSpacing: 0 }}
          >
            Security dashboard
          </Typography>
        </Box>
      </Box>

      <List component="nav" aria-label="Main navigation" disablePadding sx={{ px: 1.25 }}>
        {navigation.map((item) => {
          const isActive = location.pathname.startsWith(item.to) || (item.to === '/dashboard' && location.pathname === '/');
          if (item.to === '/alerts') {
            return (
              <Box key={item.to} sx={{ mb: 0.45 }}>
                <ListItemButton
                  component={NavLink}
                  to="/alerts?section=attention"
                  onClick={() => setIsMobileOpen(false)}
                  sx={{
                    minHeight: 46,
                    borderRadius: 1.25,
                    px: 1.25,
                    color: isActive ? '#F4EFE6' : '#8B918E',
                    bgcolor: isActive ? '#171A18' : 'transparent',
                    transition: 'background-color 180ms ease, color 180ms ease, transform 180ms ease',
                    '&:hover': { bgcolor: isActive ? '#1B1E1B' : '#111514', color: '#F4EFE6' },
                    '&:active': { transform: 'translateY(1px)' },
                    '&.Mui-focusVisible': { outline: '2px solid #D8A35D', outlineOffset: 1 },
                  }}
                >
                  <ListItemIcon sx={{ minWidth: 34, color: isActive ? '#D8A35D' : 'inherit' }}>
                    {item.icon}
                  </ListItemIcon>
                  <ListItemText primary={item.label} primaryTypographyProps={{ fontSize: 14, fontWeight: isActive ? 650 : 500, letterSpacing: '-0.01em' }} />
                  <KeyboardArrowDownIcon
                    fontSize="small"
                    sx={{ color: isActive ? '#B9A27D' : '#606663', transform: isAlertPage ? 'rotate(0deg)' : 'rotate(-90deg)', transition: 'transform 180ms ease' }}
                  />
                </ListItemButton>

                <Collapse in={isAlertPage} timeout={180} unmountOnExit>
                  <List component="div" disablePadding sx={{ position: 'relative', mt: 0.35, mb: 0.75, ml: 2.6, pl: 1.15, '&::before': { content: '""', position: 'absolute', left: 0, top: 4, bottom: 4, width: '1px', bgcolor: '#2B302E' } }}>
                    <AlertSubmenuItem
                      to="/alerts?section=attention"
                      label="Needs attention"
                      count={alertNavCounts.attention}
                      selected={alertSection === 'attention'}
                      icon={<RadioButtonUncheckedIcon sx={{ fontSize: 11 }} />}
                      onClick={() => setIsMobileOpen(false)}
                    />
                    <AlertSubmenuItem
                      to="/alerts?section=confirmed"
                      label="Confirmed"
                      count={alertNavCounts.confirmed}
                      selected={alertSection === 'confirmed'}
                      icon={<CheckCircleOutlineIcon sx={{ fontSize: 14 }} />}
                      onClick={() => setIsMobileOpen(false)}
                    />
                  </List>
                </Collapse>
              </Box>
            );
          }
          return (
            <ListItemButton
              component={NavLink}
              to={item.to}
              key={item.to}
              onClick={() => setIsMobileOpen(false)}
              sx={{
                minHeight: 46,
                borderRadius: 1.25,
                mb: 0.45,
                px: 1.25,
                color: isActive ? '#F4EFE6' : '#8B918E',
                bgcolor: isActive ? '#171A18' : 'transparent',
                transition: 'background-color 180ms ease, color 180ms ease, transform 180ms ease',
                '&:hover': {
                  bgcolor: isActive ? '#1B1E1B' : '#111514',
                  color: '#F4EFE6',
                },
                '&:active': { transform: 'translateY(1px)' },
                '&.Mui-focusVisible': { outline: '2px solid #D8A35D', outlineOffset: 1 },
              }}
            >
              <ListItemIcon
                sx={{
                  minWidth: 34,
                  color: isActive ? '#D8A35D' : 'inherit',
                  transition: 'color 180ms ease',
                }}
              >
                {item.icon}
              </ListItemIcon>
              <ListItemText
                primary={item.label}
                primaryTypographyProps={{ fontSize: 14, fontWeight: isActive ? 650 : 500, letterSpacing: '-0.01em' }}
              />
            </ListItemButton>
          );
        })}
      </List>

      <Box component="footer" sx={{ mt: 'auto', px: 1.5, pb: 1.5 }}>
        <Divider sx={{ borderColor: '#202423', mb: 1.25 }} />
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.1, px: 0.75, py: 0.6 }}>
          <Avatar
            variant="rounded"
            sx={{ width: 32, height: 32, borderRadius: 1.25, bgcolor: '#252018', color: '#E8B96F', fontSize: 13, fontWeight: 750 }}
          >
            {user?.displayName.slice(0, 1).toUpperCase() ?? 'S'}
          </Avatar>
          <Box minWidth={0} sx={{ flex: 1 }}>
            <Typography variant="body2" fontWeight={600} color="#E7E1D8" noWrap>
              {user?.displayName ?? 'Store owner'}
            </Typography>
            <Typography variant="caption" color="#6F7572">Owner</Typography>
          </Box>
          <Tooltip title="Log out" placement="top">
            <IconButton
              aria-label="Log out"
              onClick={() => void handleLogout()}
              size="small"
              sx={{
                color: '#777D7A',
                borderRadius: 1,
                transition: 'background-color 180ms ease, color 180ms ease, transform 180ms ease',
                '&:hover': { color: '#E06B65', bgcolor: 'rgba(224, 107, 101, 0.08)' },
                '&:active': { transform: 'translateY(1px)' },
                '&.Mui-focusVisible': { outline: '2px solid #D8A35D', outlineOffset: 1 },
              }}
            >
              <LogoutOutlinedIcon fontSize="small" />
            </IconButton>
          </Tooltip>
        </Box>
      </Box>
    </Box>
  );

  return (
    <Box sx={{ display: 'flex', minHeight: '100vh', bgcolor: '#090B0C', width: '100%', maxWidth: '100%', overflowX: 'hidden' }}>
      <Box component="nav" sx={{ width: { md: drawerWidth }, flexShrink: { md: 0 } }}>
        <Drawer
          variant="temporary"
          open={isMobileOpen}
          onClose={() => setIsMobileOpen(false)}
          ModalProps={{ keepMounted: true }}
          sx={{
            display: { xs: 'block', md: 'none' },
            '& .MuiDrawer-paper': { width: drawerWidth, boxSizing: 'border-box', bgcolor: '#0B0E0F' },
          }}
        >
          {drawer}
        </Drawer>
        <Drawer
          variant="permanent"
          open
          sx={{
            display: { xs: 'none', md: 'block' },
            '& .MuiDrawer-paper': { width: drawerWidth, boxSizing: 'border-box', borderRight: '1px solid #202423', bgcolor: '#0B0E0F' },
          }}
        >
          {drawer}
        </Drawer>
      </Box>
      <Box component="main" sx={{ flexGrow: 1, minWidth: 0, bgcolor: 'transparent' }}>
        {!isDesktop && (
          <AppBar position="sticky" color="transparent" elevation={0} sx={{ backdropFilter: 'blur(18px)', borderBottom: '1px solid #202423', bgcolor: 'rgba(9, 11, 12, 0.88)' }}>
            <Toolbar sx={{ minHeight: 58, px: 2 }}>
              <IconButton onClick={() => setIsMobileOpen(true)} edge="start" sx={{ color: '#F3F4F6', mr: 1 }}>
                <MenuIcon />
              </IconButton>
              <Typography fontWeight={650} color="#F4EFE6" letterSpacing="-0.02em">{currentPage}</Typography>
            </Toolbar>
          </AppBar>
        )}
        <Box sx={{ p: { xs: 2, sm: 3.5, lg: 5 }, maxWidth: 1720, mx: 'auto' }}>
          <Outlet />
        </Box>
      </Box>
    </Box>
  );
}

function AlertSubmenuItem({ to, label, count, selected, icon, onClick }: { to: string; label: string; count: number; selected: boolean; icon: React.ReactNode; onClick: () => void }) {
  return (
    <ListItemButton
      component={NavLink}
      to={to}
      onClick={onClick}
      selected={selected}
      sx={{
        minHeight: 36,
        borderRadius: 1,
        px: 1,
        py: 0.4,
        color: selected ? '#E8C38A' : '#747B77',
        '&.Mui-selected': { bgcolor: 'rgba(216,163,93,.075)' },
        '&.Mui-selected:hover': { bgcolor: 'rgba(216,163,93,.11)' },
        '&:hover': { color: '#E7E1D8', bgcolor: 'rgba(255,255,255,.025)' },
        '&.Mui-focusVisible': { outline: '2px solid #D8A35D', outlineOffset: 1 },
      }}
    >
      <ListItemIcon sx={{ minWidth: 25, color: 'inherit' }}>{icon}</ListItemIcon>
      <ListItemText primary={label} primaryTypographyProps={{ fontSize: 12.5, fontWeight: selected ? 650 : 500 }} />
      <Typography variant="caption" sx={{ minWidth: 23, px: 0.65, py: 0.05, borderRadius: 0.75, textAlign: 'center', color: selected ? '#F0C987' : '#6F7572', bgcolor: selected ? 'rgba(216,163,93,.12)' : '#151918', fontVariantNumeric: 'tabular-nums' }}>
        {count}
      </Typography>
    </ListItemButton>
  );
}
