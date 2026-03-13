import { useEffect, useState } from 'react'
import ReactMarkdown from 'react-markdown'
import {
  Card,
  Input,
  Button,
  List,
  Typography,
  Space,
  Tag,
  Select,
  Empty,
  Spin,
  Slider,
  Switch,
  Alert,
  Collapse,
  Progress,
  Tooltip,
  message,
} from 'antd'
import {
  SearchOutlined,
  FileTextOutlined,
  BranchesOutlined,
  BulbOutlined,
  InfoCircleOutlined,
  RobotOutlined,
} from '@ant-design/icons'
import { KnowledgeBase, RagRequest, RagResponse, RagResult, ResultChunk } from '@/types'
import { getKnowledgeBases, ragQuery } from '@/api/knowledge'
import WordCloud from '@/components/WordCloud'

const { Title, Text, Paragraph } = Typography
const { TextArea } = Input

const sourceConfig: Record<string, { icon: React.ReactNode; color: string; label: string }> = {
  text: { icon: <FileTextOutlined />, color: 'blue', label: '文本' },
  vector: { icon: <BulbOutlined />, color: 'green', label: '语义' },
  graph: { icon: <BranchesOutlined />, color: 'purple', label: '图谱' },
}

const RagQueryPage = () => {
  const [query, setQuery] = useState('')
  const [loading, setLoading] = useState(false)
  const [response, setResponse] = useState<RagResponse | null>(null)
  const [knowledgeBases, setKnowledgeBases] = useState<KnowledgeBase[]>([])
  const [selectedKnowledgeBaseId, setSelectedKnowledgeBaseId] = useState<string>()
  const [topK, setTopK] = useState(10)
  const [hybridWeight, setHybridWeight] = useState(0.5)
  const [includeGraph, setIncludeGraph] = useState(false)

  useEffect(() => {
    void fetchKnowledgeBases()
  }, [])

  const results = response?.results ?? []
  const understandingKeywords = response?.understanding?.keywords ?? []

  const fetchKnowledgeBases = async () => {
    try {
      const kbs = await getKnowledgeBases()
      setKnowledgeBases(kbs)
      if (!selectedKnowledgeBaseId && kbs.length > 0) {
        setSelectedKnowledgeBaseId(kbs[0].id)
      }
    } catch {
      message.error('获取知识库列表失败')
    }
  }

  const handleSearch = async () => {
    if (!query.trim()) return
    if (!selectedKnowledgeBaseId) {
      message.error('请先选择知识库')
      return
    }

    setLoading(true)
    try {
      const request: RagRequest = {
        query,
        knowledge_base_id: selectedKnowledgeBaseId,
        top_k: topK,
        hybrid_weight: hybridWeight,
        include_graph: includeGraph,
      }
      const result = await ragQuery(request)
      setResponse(result)
    } catch {
      message.error('检索失败')
    } finally {
      setLoading(false)
    }
  }

  const getRelevanceColor = (relevance: number) => {
    if (relevance >= 80) return '#52c41a'
    if (relevance >= 50) return '#1890ff'
    if (relevance >= 30) return '#faad14'
    return '#ff4d4f'
  }

  const renderSourceTags = (sources: string[]) => (
    <Space size={4}>
      {sources.map((src) => {
        const cfg = sourceConfig[src] || { icon: <FileTextOutlined />, color: 'default', label: src }
        return (
          <Tag key={src} icon={cfg.icon} color={cfg.color} style={{ marginRight: 0 }}>
            {cfg.label}
          </Tag>
        )
      })}
    </Space>
  )

  const renderChunkDetail = (chunk: ResultChunk, idx: number) => (
    <div key={chunk.chunk_id || idx} style={{ padding: '8px 0', borderBottom: '1px solid #f0f0f0' }}>
      <Space size="small" style={{ marginBottom: 4 }}>
        <Tag color="default" style={{ fontSize: 11 }}>
          片段 {chunk.chunk_index + 1}/{chunk.total_chunks}
        </Tag>
        {sourceConfig[chunk.source] && (
          <Tag color={sourceConfig[chunk.source].color} style={{ fontSize: 11 }}>
            {sourceConfig[chunk.source].label}召回
          </Tag>
        )}
      </Space>
      {chunk.context?.prev_content && (
        <Paragraph type="secondary" style={{ fontSize: 12, marginBottom: 4, fontStyle: 'italic' }} ellipsis={{ rows: 1 }}>
          ...{chunk.context.prev_content}
        </Paragraph>
      )}
      <Paragraph style={{ marginBottom: 4 }} ellipsis={{ rows: 3, expandable: true, symbol: '展开' }}>
        {chunk.highlights && chunk.highlights.length > 0
          ? chunk.highlights.join('\n')
          : chunk.content}
      </Paragraph>
      {chunk.context?.next_content && (
        <Paragraph type="secondary" style={{ fontSize: 12, marginBottom: 0, fontStyle: 'italic' }} ellipsis={{ rows: 1 }}>
          {chunk.context.next_content}...
        </Paragraph>
      )}
    </div>
  )

  return (
    <div>
      <div style={{ marginBottom: 20 }}>
        <Title level={4} style={{ margin: 0 }}>智能检索</Title>
        <Text type="secondary" style={{ fontSize: 13 }}>使用 RAG 混合检索 + AI 总结，快速获取精准答案</Text>
      </div>

      <Card style={{ marginBottom: 24 }}>
        <Space direction="vertical" style={{ width: '100%' }} size="large">
          <Select
            placeholder="选择知识库"
            style={{ width: 320 }}
            value={selectedKnowledgeBaseId}
            onChange={setSelectedKnowledgeBaseId}
            options={knowledgeBases.map((kb) => ({ label: kb.name, value: kb.id }))}
          />

          <TextArea
            placeholder="输入您的查询，例如：如何部署微服务？"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            autoSize={{ minRows: 3, maxRows: 6 }}
            onKeyDown={(e) => {
              if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
                e.preventDefault()
                handleSearch()
              }
            }}
            disabled={!selectedKnowledgeBaseId}
          />

          <Space wrap>
            <Text>返回结果数: {topK}</Text>
            <Slider min={5} max={50} value={topK} onChange={setTopK} style={{ width: 200 }} />
          </Space>

          <Space wrap>
            <Text>混合检索权重: {hybridWeight.toFixed(1)}</Text>
            <Slider min={0} max={1} step={0.1} value={hybridWeight} onChange={setHybridWeight} style={{ width: 200 }} />
            <Text type="secondary">(文本: {(1 - hybridWeight).toFixed(1)} / 向量: {hybridWeight.toFixed(1)})</Text>
          </Space>

          <Space>
            <Switch checked={includeGraph} onChange={setIncludeGraph} />
            <Text>包含图谱检索</Text>
          </Space>

          <Button
            type="primary"
            icon={<SearchOutlined />}
            onClick={handleSearch}
            loading={loading}
            size="large"
            disabled={!selectedKnowledgeBaseId}
          >
            检索 (⌘Enter)
          </Button>
        </Space>
      </Card>

      {loading ? (
        <div style={{ textAlign: 'center', padding: 50 }}>
          <Spin size="large" />
        </div>
      ) : response ? (
        <div>
          {response.understanding && (
            <Alert
              type="info"
              message="查询理解"
              description={
                <Space direction="vertical" size="small" style={{ width: '100%' }}>
                  <Text>意图: {response.understanding.intent}</Text>
                  {understandingKeywords.length > 0 && (
                    <div>
                      <Text type="secondary" style={{ marginRight: 8 }}>关键词:</Text>
                      <WordCloud words={understandingKeywords} maxWords={15} height={80} />
                    </div>
                  )}
                  <Text>
                    路由: <Tag color="blue">{response.routing}</Tag>
                  </Text>
                </Space>
              }
              style={{ marginBottom: 16 }}
            />
          )}

          {response.answer && (
            <Card
              style={{
                marginBottom: 16,
                borderRadius: 12,
                border: '1px solid #c7d2fe',
                background: 'linear-gradient(135deg, #eef2ff 0%, #f0f9ff 100%)',
              }}
              bodyStyle={{ padding: '16px 20px' }}
            >
              <div style={{ display: 'flex', gap: 12 }}>
                <div
                  style={{
                    width: 36,
                    height: 36,
                    borderRadius: 10,
                    background: 'linear-gradient(135deg, #6366f1, #8b5cf6)',
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    flexShrink: 0,
                  }}
                >
                  <RobotOutlined style={{ color: '#fff', fontSize: 18 }} />
                </div>
                <div style={{ flex: 1 }}>
                  <Text strong style={{ fontSize: 14, color: '#3730a3', display: 'block', marginBottom: 8 }}>
                    AI 总结回答
                  </Text>
                  <div
                    className="ai-answer-markdown"
                    style={{
                      fontSize: 13,
                      lineHeight: 1.8,
                      color: '#262626',
                    }}
                  >
                    <ReactMarkdown>{response.answer}</ReactMarkdown>
                  </div>
                  <div style={{ marginTop: 8 }}>
                    <Text type="secondary" style={{ fontSize: 11 }}>
                      <BulbOutlined style={{ marginRight: 4 }} />
                      基于检索到的 {results.length} 篇文档生成，仅供参考
                    </Text>
                  </div>
                </div>
              </div>
            </Card>
          )}

          {response.graph_info && response.graph_info.entities && (
            <Card title="相关实体" style={{ marginBottom: 16 }}>
              <Space wrap>
                {response.graph_info.entities.map((entity, idx) => (
                  <Tag key={idx} icon={<BranchesOutlined />} color="purple">
                    {entity.name} ({entity.type})
                  </Tag>
                ))}
              </Space>
            </Card>
          )}

          <Card title={`检索结果 (${results.length} 篇文档)`}>
            <List
              itemLayout="vertical"
              dataSource={results}
              renderItem={(item: RagResult) => (
                <List.Item key={item.document_id}>
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
                    <div style={{ flex: 1 }}>
                      <Space align="center" style={{ marginBottom: 8 }}>
                        <Text strong style={{ fontSize: 16 }}>{item.title}</Text>
                        <Tag>{item.doc_type}</Tag>
                        {renderSourceTags(item.sources || [])}
                      </Space>
                    </div>
                    <div style={{ textAlign: 'right', minWidth: 120 }}>
                      <Tooltip title={`RRF分数: ${item.score.toFixed(6)}`}>
                        <div>
                          <Progress
                            type="circle"
                            percent={item.relevance}
                            size={50}
                            strokeColor={getRelevanceColor(item.relevance)}
                            format={(pct) => `${pct}`}
                          />
                        </div>
                      </Tooltip>
                      <Text type="secondary" style={{ fontSize: 11 }}>相关度</Text>
                    </div>
                  </div>

                  {item.document_summary && (
                    <div style={{ background: '#f6f8fa', padding: '8px 12px', borderRadius: 6, marginBottom: 8 }}>
                      <Text type="secondary" style={{ fontSize: 12 }}>
                        <InfoCircleOutlined style={{ marginRight: 4 }} />
                        文档摘要：{item.document_summary}
                      </Text>
                    </div>
                  )}

                  <Paragraph ellipsis={{ rows: 3, expandable: true, symbol: '展开' }} style={{ marginBottom: 8 }}>
                    {item.highlights && item.highlights.length > 0
                      ? item.highlights.join('\n')
                      : item.content}
                  </Paragraph>

                  {item.chunks && item.chunks.length > 1 && (
                    <Collapse
                      size="small"
                      items={[{
                        key: '1',
                        label: (
                          <Text type="secondary" style={{ fontSize: 12 }}>
                            共匹配 {item.chunks.length} 个片段，点击展开查看
                          </Text>
                        ),
                        children: item.chunks.map((chunk, idx) => renderChunkDetail(chunk, idx)),
                      }]}
                    />
                  )}
                </List.Item>
              )}
            />
          </Card>
        </div>
      ) : (
        <Empty description={selectedKnowledgeBaseId ? '请输入查询开始检索' : '请先选择知识库'} />
      )}
    </div>
  )
}

export default RagQueryPage
