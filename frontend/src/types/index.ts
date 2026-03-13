export interface KnowledgeBase {
  id: string
  name: string
  description: string
  status: string
  created_at: string
  updated_at: string
}

export interface Document {
  id: string
  knowledge_base_id?: string
  title: string
  content: string
  summary?: string
  doc_type: string
  format: string
  file_path?: string
  file_size?: number
  status: string
  metadata?: Record<string, unknown>
  semantic_metadata?: Record<string, unknown>
  created_at: string
  updated_at: string
}

export interface ProcessingEvent {
  id: string
  document_id: string
  step: number
  stage: string
  status: 'started' | 'success' | 'warning' | 'failed'
  message: string
  details?: Record<string, unknown>
  created_at: string
}

export interface RagRequest {
  query: string
  knowledge_base_id?: string
  top_k?: number
  filters?: Record<string, unknown>
  hybrid_weight?: number
  include_graph?: boolean
}

export interface KBChunkSample {
  chunk_id: string
  document_id: string
  chunk_index: number
  chunk_type: string
  vector_id: string
  content: string
}

export interface KBTextIndexSample {
  id: string
  document_id: string
  knowledge_base_id: string
  title: string
  content: string
  doc_type: string
  updated_at: string
  metadata?: Record<string, unknown>
}

export interface KBVectorIndexSample {
  id: string
  document_id: string
  knowledge_base_id: string
  title: string
  content: string
  embedding_dim: number
  embedding_preview: number[]
}

export interface KBGraphEntitySample {
  entity_id: string
  document_id: string
  name: string
  type: string
}

export interface KBGraphRelationSample {
  relation_id: string
  source_entity_id: string
  source_name: string
  source_type: string
  target_entity_id: string
  target_name: string
  target_type: string
  relation_type: string
  weight: number
  source_document_id: string
  target_document_id: string
}

// === Document Observability Types ===

export interface TokenInfo {
  token: string
  start_offset: number
  end_offset: number
  type: string
  position: number
}

export interface TokenAnalysisStats {
  total_tokens: number
  unique_tokens: number
  token_types: Record<string, number>
  avg_token_len: number
}

export interface TokenAnalysisResult {
  analyzer: string
  tokens: TokenInfo[]
  stats: TokenAnalysisStats
}

export interface ChunkDetail {
  chunk_id: string
  chunk_index: number
  chunk_type: string
  vector_id: string
  content: string
  word_count: number
}

export interface DocumentIndexStatus {
  document_id: string
  in_text_index: boolean
  in_vector_index: boolean
  text_index_count: number
  vector_index_count: number
}

export interface DocumentVectorSample {
  id: string
  chunk_id: string
  content: string
  embedding_dim: number
  embedding_full: number[]
}

export interface DocumentObservability {
  document_id: string
  title: string
  content: string
  doc_type: string
  status: string
  index_status: DocumentIndexStatus | null
  chunks: ChunkDetail[]
  token_analysis: TokenAnalysisResult | null
  vector_samples: DocumentVectorSample[]
  graph_entities: KBGraphEntitySample[]
  graph_relations: KBGraphRelationSample[]
  llm_usage: DocumentLLMUsageSummary | null
  warnings?: string[]
}

// === LLM Usage Types ===

export interface MethodUsageStat {
  caller_service: string
  caller_method: string
  calls: number
  input_tokens: number
  output_tokens: number
  total_tokens: number
  cost_usd: number
}

export interface ModelTypeUsage {
  model_type: string
  model_id: string
  calls: number
  input_tokens: number
  output_tokens: number
  total_tokens: number
  cost_usd: number
}

export interface LLMUsageItem {
  id: string
  caller_service: string
  caller_method: string
  model_id: string
  model_type: string
  input_tokens: number
  output_tokens: number
  total_tokens: number
  cost_usd: number
  duration_ms: number
  status: string
  created_at: string
}

export interface DocumentLLMUsageSummary {
  total_calls: number
  total_input_tokens: number
  total_output_tokens: number
  total_tokens: number
  estimated_cost_usd: number
  by_method: MethodUsageStat[]
  by_model_type: ModelTypeUsage[]
  records: LLMUsageItem[]
}

export interface ServiceUsageStat {
  caller_service: string
  calls: number
  input_tokens: number
  output_tokens: number
  total_tokens: number
  cost_usd: number
}

export interface DocUsageStat {
  document_id: string
  document_title: string
  calls: number
  total_tokens: number
  cost_usd: number
}

export interface KBLLMUsageSummary {
  total_calls: number
  total_input_tokens: number
  total_output_tokens: number
  total_tokens: number
  estimated_cost_usd: number
  by_service: ServiceUsageStat[]
  by_model_type: ModelTypeUsage[]
  top_documents: DocUsageStat[]
}

export interface KnowledgeBaseObservability {
  knowledge_base_id: string
  stats: Record<string, number>
  chunk_samples: KBChunkSample[]
  text_index_samples: KBTextIndexSample[]
  vector_index_samples: KBVectorIndexSample[]
  graph_entity_samples: KBGraphEntitySample[]
  graph_relation_samples: KBGraphRelationSample[]
  llm_usage: KBLLMUsageSummary | null
  warnings?: string[]
}

export interface RagResult {
  document_id: string
  title: string
  content: string                    // primary/best chunk content
  document_summary?: string
  chunks?: ResultChunk[]             // all matched chunks from this doc
  relevance: number                  // 0-100 human-readable relevance %
  score: number                      // raw RRF score (for debugging)
  sources: string[]                  // all channels: text, vector, graph
  doc_type: string
  metadata?: Record<string, unknown>
  highlights?: string[]
  related_docs?: string[]
}

export interface ResultChunk {
  chunk_id: string
  content: string
  chunk_index: number
  total_chunks: number
  score: number
  source: string
  highlights?: string[]
  context?: ChunkContext
}

export interface ChunkContext {
  prev_content?: string
  next_content?: string
}

export interface RagResponse {
  query: string
  answer?: string
  understanding?: QueryUnderstanding
  results: RagResult[]
  graph_info?: GraphInfo
  routing: string
}

export interface QueryUnderstanding {
  intent: string
  entities: string[]
  time_filter?: Record<string, unknown>
  type_filter?: string
  keywords: string[]
  routing: string
  rewritten?: string
}

export interface GraphInfo {
  entities?: EntityInfo[]
  related_paths?: PathInfo[]
  related_concepts?: string[]
}

export interface EntityInfo {
  name: string
  type: string
  related_to?: string[]
}

export interface PathInfo {
  source: string
  target: string
  path: string[]
}
