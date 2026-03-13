import { create } from 'zustand'
import { persist } from 'zustand/middleware'

interface AuthState {
  tenantId: string | null
  setTenant: (tenantId: string) => void
  logout: () => void
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      tenantId: null,
      setTenant: (tenantId) => set({ tenantId }),
      logout: () => set({ tenantId: null }),
    }),
    {
      name: 'auth-storage',
    }
  )
)
