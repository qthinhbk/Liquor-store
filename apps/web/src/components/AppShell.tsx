import CameraAltOutlinedIcon from '@mui/icons-material/CameraAltOutlined';
import GridViewOutlinedIcon from '@mui/icons-material/GridViewOutlined';
import LiquorOutlinedIcon from '@mui/icons-material/LiquorOutlined';
import LogoutOutlinedIcon from '@mui/icons-material/LogoutOutlined';
import MenuIcon from '@mui/icons-material/Menu';
import NotificationsActiveOutlinedIcon from '@mui/icons-material/NotificationsActiveOutlined';
import { AppBar, Avatar, Box, Divider, Drawer, IconButton, List, ListItemButton, ListItemIcon, ListItemText, Toolbar, Tooltip, Typography, useMediaQuery } from '@mui/material';
import { useTheme } from '@mui/material/styles';
import { useState } from 'react';
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom';
import { useAuth } from '../auth/AuthProvider';

const drawerWidth = 232;

const navigation = [
  { label: 'Overview', to: '/dashboard', icon: <GridViewOutlinedIcon fontSize="small" /> },
  { label: 'Alerts', to: '/alerts', icon: <NotificationsActiveOutlinedIcon fontSize="small" /> },
  { label: 'Cameras', to: '/cameras', icon: <CameraAltOutlinedIcon fontSize="small" /> },
];

export function AppShell() {
  const theme = useTheme();
  const isDesktop = useMediaQuery(theme.breakpoints.up('md'));
  const [isMobileOpen, setIsMobileOpen] = useState(false);
  const { user, logout } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const currentPage = navigation.find((item) => location.pathname.startsWith(item.to))?.label ?? 'Overview';

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
