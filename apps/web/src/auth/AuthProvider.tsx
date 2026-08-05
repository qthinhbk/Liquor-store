import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import { api } from '../lib/api';
import type { CurrentUser, Store } from '../lib/types';

interface AuthContextValue {
  user: CurrentUser | null;
  store: Store | null;
  isLoading: boolean;
  login: (email: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthContextValue | undefined>(undefined);

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [user, setUser] = useState<CurrentUser | null>(null);
  const [store, setStore] = useState<Store | null>(null);
  const [isLoading, setIsLoading] = useState(true);

  const establishSession = useCallback(async (session: { user: CurrentUser }) => {
    const stores = await api.getStores();
    const firstStore = stores[0] ?? null;
    setUser(session.user);
    setStore(firstStore);
  }, []);

  useEffect(() => {
    void (async () => {
      try {
        await establishSession(await api.refresh());
      } catch {
        setUser(null);
        setStore(null);
      } finally {
        setIsLoading(false);
      }
    })();
  }, [establishSession]);

  const value = useMemo<AuthContextValue>(() => ({
    user,
    store,
    isLoading,
    login: async (email, password) => {
      await establishSession(await api.login(email, password));
    },
    logout: async () => {
      await api.logout();
      setUser(null);
      setStore(null);
    },
  }), [establishSession, isLoading, store, user]);

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  const context = useContext(AuthContext);
  if (!context) throw new Error('useAuth must be used inside AuthProvider.');
  return context;
}
