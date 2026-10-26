import axios, { AxiosError } from 'axios'
import { clearSession, getToken } from './auth'
import { API_BASE } from './config'

export class ApiError extends Error {
  readonly status: number

  constructor(message: string, status: number) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

export const http = axios.create({
  baseURL: API_BASE,
  timeout: 30_000,
  headers: { Accept: 'application/json' },
})

http.interceptors.request.use((config) => {
  const token = getToken()
  if (token) {
    config.headers.set('Authorization', `Bearer ${token}`)
  }
  return config
})

http.interceptors.response.use(
  (response) => response,
  (error: AxiosError<{ error?: unknown }>) => {
    const status = error.response?.status ?? 0
    const isLogin = error.config?.url?.includes('/api/login') ?? false
    if (status === 401 && !isLogin) {
      clearSession()
      if (window.location.pathname !== '/login') {
        const redirect = `${window.location.pathname}${window.location.search}`
        window.location.assign(`/login?redirect=${encodeURIComponent(redirect)}`)
      }
    }

    const body = error.response?.data
    let message = ''
    if (body && typeof body === 'object' && typeof body.error === 'string') {
      message = body.error
    } else if (typeof body === 'string') {
      message = body
    }
    if (!message) {
      if (error.code === 'ECONNABORTED') {
        message = '請求逾時，請稍後重試'
      } else if (status > 0) {
        message = `HTTP ${status}`
      } else {
        message = error.message || '網路請求失敗'
      }
    }
    return Promise.reject(new ApiError(message, status))
  },
)
