import React, { Suspense } from 'react'
import ReactDOM from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import App from './App'
import { ThemeProvider } from './theme'
import './style.css'
import './mobile.css'
const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 5000, refetchOnWindowFocus: false } } })
ReactDOM.createRoot(document.getElementById('root')!).render(<React.StrictMode><ThemeProvider><QueryClientProvider client={queryClient}><Suspense fallback={<div className="app-loading">正在载入栈长…</div>}><App /></Suspense></QueryClientProvider></ThemeProvider></React.StrictMode>)
