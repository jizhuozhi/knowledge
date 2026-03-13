import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import MainLayout from './layouts/MainLayout'
import KnowledgeBase from './pages/KnowledgeBase'
import DocumentList from './pages/DocumentList'
import DocumentDetail from './pages/DocumentDetail'
import RagQuery from './pages/RagQuery'
import KnowledgeObservability from './pages/KnowledgeObservability'
import TenantSettings from './pages/TenantSettings'
import { useAuthStore } from './stores/authStore'

function App() {
  const { tenantId } = useAuthStore()

  if (!tenantId) {
    return <TenantSettings />
  }

  return (
    <BrowserRouter>
      <MainLayout>
        <Routes>
          <Route path="/" element={<Navigate to="/knowledge" replace />} />
          <Route path="/knowledge" element={<KnowledgeBase />} />
          <Route path="/documents" element={<DocumentList />} />
          <Route path="/documents/:id" element={<DocumentDetail />} />
          <Route path="/rag" element={<RagQuery />} />
          <Route path="/observability" element={<KnowledgeObservability />} />
          <Route path="/settings" element={<TenantSettings />} />
          <Route path="*" element={<Navigate to="/knowledge" replace />} />
        </Routes>
      </MainLayout>
    </BrowserRouter>
  )
}

export default App
