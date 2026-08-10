import { createTheme } from '@mui/material/styles';

export const theme = createTheme({
  palette: {
    mode: 'dark',
    primary: {
      main: '#D8A35D',
      light: '#F0C987',
      dark: '#A86F2F',
      contrastText: '#130F0A',
    },
    secondary: {
      main: '#91B3A2',
      light: '#B8D1C4',
      dark: '#5F8975',
    },
    background: {
      default: '#090B0C',
      paper: '#111516',
    },
    text: {
      primary: '#F4EFE6',
      secondary: '#989C99',
    },
    divider: '#252A29',
    success: { main: '#78B990' },
    warning: { main: '#D8A35D' },
    error: { main: '#E06B65' },
    info: { main: '#72A7B5' },
  },
  shape: { borderRadius: 6 },
  typography: {
    fontFamily: 'Geist, "Aptos", "Segoe UI", sans-serif',
    h4: { fontWeight: 520, letterSpacing: '-0.045em' },
    h5: { fontWeight: 560, letterSpacing: '-0.035em' },
    h6: { fontWeight: 600, letterSpacing: '-0.02em' },
    button: { letterSpacing: '-0.01em' },
  },
  components: {
    MuiCssBaseline: {
      styleOverrides: {
        body: {
          backgroundColor: '#090B0C',
          color: '#F4EFE6',
          backgroundImage: 'radial-gradient(circle at 82% -10%, rgba(216, 163, 93, 0.09), transparent 32%)',
          scrollbarColor: '#343A38 #090B0C',
        },
        '::selection': { backgroundColor: '#D8A35D', color: '#130F0A' },
      },
    },
    MuiPaper: {
      styleOverrides: {
        root: {
          backgroundImage: 'none',
          backgroundColor: '#111516',
          borderColor: '#252A29',
        },
      },
    },
    MuiCard: {
      styleOverrides: {
        root: {
          backgroundImage: 'none',
          backgroundColor: '#111516',
          border: '1px solid #252A29',
          borderRadius: 6,
          transition: 'transform 260ms ease, border-color 260ms ease, box-shadow 260ms ease',
        },
      },
    },
    MuiButton: {
      styleOverrides: {
        root: {
          borderRadius: 4,
          textTransform: 'none',
          fontWeight: 650,
          minHeight: 40,
        },
      },
    },
    MuiChip: {
      styleOverrides: {
        root: { borderRadius: 3, fontWeight: 650 },
      },
    },
  },
});
