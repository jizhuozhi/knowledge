import api from './client'
import { KnowledgeBase, Document, ProcessingEvent, RagRequest, RagResponse, KnowledgeBaseObservability, DocumentObservability } from '@/types'

// Knowledge Base APIs
export const getKnowledgeBases = async () => {
  const response = await api.get<{ data: KnowledgeBase[]; total: number }>('/knowledge-bases')
  return response.data.data || []
}

export const getKnowledgeBaseObservability = async (id: string, limit = 20) => {
  const response = await api.get<KnowledgeBaseObservability>(`/knowledge-bases/${id}/observability`, {
    params: { limit },
  })
  return response.data
}

export const createKnowledgeBase = async (data: Partial<KnowledgeBase>) => {
  const response = await api.post<KnowledgeBase>('/knowledge-bases', data)
  return response.data
}

export const deleteKnowledgeBase = async (id: string) => {
  await api.delete(`/knowledge-bases/${id}`)
}

// Document APIs
export const getDocuments = async (params?: {
  knowledge_base_id?: string
  doc_type?: string
  keyword?: string
  page?: number
  page_size?: number
}) => {
  const response = await api.get<{ data: Document[]; total: number }>('/documents', { params })
  return response.data
}

export const getDocument = async (id: string) => {
  const response = await api.get<Document>(`/documents/${id}`)
  return response.data
}

export const createDocument = async (data: Partial<Document>) => {
  const response = await api.post<Document>('/documents', data)
  return response.data
}

export const uploadDocument = async (file: File, metadata?: Record<string, unknown>) => {
  const formData = new FormData()
  formData.append('file', file)
  if (metadata) {
    formData.append('metadata', JSON.stringify(metadata))
  }
  const response = await api.post<Document>('/documents/upload', formData)
  return response.data
}

export const processDocument = async (id: string) => {
  const response = await api.post<{ message: string; document_id: string }>(`/documents/${id}/process`)
  return response.data
}

export const getDocumentProcessingEvents = async (id: string) => {
  const response = await api.get<{ data: ProcessingEvent[]; total: number }>(`/documents/${id}/processing-events`)
  return response.data
}

export const deleteDocument = async (id: string) => {
  await api.delete(`/documents/${id}`)
}

export const getDocumentObservability = async (id: string) => {
  const response = await api.get<DocumentObservability>(`/documents/${id}/observability`)
  return response.data
}

// RAG APIs
export const ragQuery = async (request: RagRequest) => {
  const response = await api.post<RagResponse>('/rag/query', request)
  return response.data
}

// Tenant APIs
export const createTenant = async (data: { name: string; code: string; description?: string }) => {
  const response = await api.post('/admin/tenants', data)
  return response.data
}

export const listTenants = async () => {
  const response = await api.get<{ data: Array<{ id: string; name: string; code: string; description: string; status: string; created_at: string }>; total: number }>('/admin/tenants')
  return response.data
}
