import axios from 'axios'
import { useAuthStore } from '@/stores/authStore'

const api = axios.create({
  baseURL: '/api/v1',
  timeout: 30000,
})

// Request interceptor — inject tenant ID header
api.interceptors.request.use((config) => {
  const { tenantId } = useAuthStore.getState()
  if (tenantId) {
    config.headers['X-Tenant-ID'] = tenantId
  }
  return config
})

export default api
