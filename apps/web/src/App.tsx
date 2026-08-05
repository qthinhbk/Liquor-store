import { BrowserRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom';
import { AuthProvider, useAuth } from './auth/AuthProvider';
import { AppShell } from './components/AppShell';
import { ScreenLoader } from './components/common';
import { AlertsPage } from './pages/AlertsPage';
import { CamerasPage } from './pages/CamerasPage';
import { DashboardPage } from './pages/DashboardPage';
import { LoginPage } from './pages/LoginPage';

function ProtectedLayout() {
  const { user, isLoading } = useAuth();
  const location = useLocation();
  if (isLoading) return <ScreenLoader label="Restoring your secure session…" />;
  if (!user) return <Navigate to="/login" replace state={{ from: location }} />;
  return <AppShell />;
}

function RootRedirect() {
  const { user, isLoading } = useAuth();
  if (isLoading) return <ScreenLoader />;
  return <Navigate to={user ? '/dashboard' : '/login'} replace />;
}

export function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route element={<ProtectedLayout />}>
            <Route path="/dashboard" element={<DashboardPage />} />
            <Route path="/alerts" element={<AlertsPage />} />
            <Route path="/cameras" element={<CamerasPage />} />
          </Route>
          <Route path="*" element={<RootRedirect />} />
        </Routes>
      </AuthProvider>
    </BrowserRouter>
  );
}
