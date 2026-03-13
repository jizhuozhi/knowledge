import { useEffect, useMemo, useRef, useState } from 'react'
import {
  Alert,
  Badge,
  Card,
  Col,
  Descriptions,
  Empty,
  Progress,
  Row,
  Space,
  Spin,
  Statistic,
  Table,
  Tabs,
  Tag,
  Typography,
  message,
} from 'antd'
import {
  CheckCircleOutlined,
  CloseCircleOutlined,
  DollarOutlined,
  InfoCircleOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import { getDocumentObservability } from '@/api/knowledge'
import type {
  DocumentObservability,
  ChunkDetail,
  TokenInfo,
  DocumentVectorSample,
  KBGraphEntitySample,
  KBGraphRelationSample,
  MethodUsageStat,
  LLMUsageItem,
} from '@/types'
import GraphVisualization from './GraphVisualization'

const { Text, Paragraph } = Typography

interface Props {
  documentId: string
}

// ============================================================
// Token Cloud — Canvas-based word cloud with spiral placement
// ============================================================
const TYPE_COLORS: Record<string, string> = {
  CN_WORD: '#1890ff',
  CN_CHAR: '#52c41a',
  ENGLISH: '#722ed1',
  ARABIC: '#fa8c16',
  TYPE_CNUM: '#13c2c2',
  LETTER: '#eb2f96',
  OTHER: '#8c8c8c',
}

interface CloudWord {
  token: string
  type: string
  count: number
  fontSize: number
  color: string
}

const TokenCloud = ({ tokens }: { tokens: TokenInfo[] }) => {
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const tooltipRef = useRef<HTMLDivElement>(null)
  const wordsRef = useRef<Array<CloudWord & { x: number; y: number; width: number; height: number }>>([])

  const cloudWords = useMemo<CloudWord[]>(() => {
    if (!Array.isArray(tokens) || tokens.length === 0) return []

    const freq = new Map<string, { token: string; type: string; count: number }>()
    for (const t of tokens) {
      const key = `${t.token}__${t.type}`
      const existing = freq.get(key)
      if (existing) existing.count++
      else freq.set(key, { token: t.token, type: t.type, count: 1 })
    }

    const sorted = Array.from(freq.values()).sort((a, b) => b.count - a.count).slice(0, 80)
    if (sorted.length === 0) return []

    const maxCount = sorted[0].count
    const minCount = sorted[sorted.length - 1].count
    const range = maxCount - minCount || 1

    return sorted.map(({ token, type, count }) => {
      const norm = (count - minCount) / range
      const fontSize = Math.round(12 + norm * 24) // 12px → 36px
      const color = TYPE_COLORS[type] || TYPE_COLORS.OTHER
      return { token, type, count, fontSize, color }
    })
  }, [tokens])

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas || cloudWords.length === 0) return
    const ctx = canvas.getContext('2d')
    if (!ctx) return

    const W = canvas.width
    const H = canvas.height
    const centerX = W / 2
    const centerY = H / 2

    ctx.clearRect(0, 0, W, H)

    // Occupied bounding boxes for collision detection
    const placed: Array<{ x: number; y: number; w: number; h: number }> = []
    const placedWords: typeof wordsRef.current = []

    const collides = (x: number, y: number, w: number, h: number) => {
      for (const b of placed) {
        if (!(x + w < b.x || x > b.x + b.w || y + h < b.y || y > b.y + b.h)) return true
      }
      return false
    }

    for (const word of cloudWords) {
      ctx.font = `${word.fontSize > 24 ? 'bold ' : ''}${word.fontSize}px -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif`
      const metrics = ctx.measureText(word.token)
      const w = metrics.width + 6
      const h = word.fontSize + 4

      // Archimedean spiral placement
      let placed_ = false
      for (let t = 0; t < 600; t++) {
        const angle = t * 0.15
        const radius = 2 + t * 0.45
        const x = centerX + radius * Math.cos(angle) - w / 2
        const y = centerY + radius * Math.sin(angle) - h / 2

        if (x < 0 || y < 0 || x + w > W || y + h > H) continue
        if (!collides(x, y, w, h)) {
          placed.push({ x, y, w, h })
          ctx.fillStyle = word.color
          ctx.font = `${word.fontSize > 24 ? 'bold ' : ''}${word.fontSize}px -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif`
          ctx.textBaseline = 'top'
          ctx.fillText(word.token, x + 3, y + 2)
          placedWords.push({ ...word, x, y, width: w, height: h })
          placed_ = true
          break
        }
      }

      if (!placed_) {
        // Couldn't place — skip
      }
    }

    wordsRef.current = placedWords
  }, [cloudWords])

  const handleMouseMove = (e: React.MouseEvent<HTMLCanvasElement>) => {
    const canvas = canvasRef.current
    const tooltip = tooltipRef.current
    if (!canvas || !tooltip) return

    const rect = canvas.getBoundingClientRect()
    const mx = e.clientX - rect.left
    const my = e.clientY - rect.top

    const hit = wordsRef.current.find(
      (w) => mx >= w.x && mx <= w.x + w.width && my >= w.y && my <= w.y + w.height
    )

    if (hit) {
      tooltip.style.display = 'block'
      tooltip.style.left = `${e.clientX - rect.left + 10}px`
      tooltip.style.top = `${e.clientY - rect.top - 30}px`
      tooltip.textContent = `${hit.token}  ·  类型: ${hit.type}  ·  出现 ${hit.count} 次`
      canvas.style.cursor = 'pointer'
    } else {
      tooltip.style.display = 'none'
      canvas.style.cursor = 'default'
    }
  }

  const handleMouseLeave = () => {
    if (tooltipRef.current) tooltipRef.current.style.display = 'none'
  }

  if (!Array.isArray(tokens) || tokens.length === 0) {
    return <Empty description="无分词数据" />
  }

  return (
    <div style={{ position: 'relative' }}>
      <canvas
        ref={canvasRef}
        width={720}
        height={320}
        onMouseMove={handleMouseMove}
        onMouseLeave={handleMouseLeave}
        style={{
          width: '100%',
          maxWidth: 720,
          height: 'auto',
          borderRadius: 8,
          border: '1px solid #f0f0f0',
          background: '#fafbfc',
        }}
      />
      <div
        ref={tooltipRef}
        style={{
          display: 'none',
          position: 'absolute',
          background: 'rgba(0,0,0,0.75)',
          color: '#fff',
          padding: '4px 10px',
          borderRadius: 4,
          fontSize: 12,
          pointerEvents: 'none',
          whiteSpace: 'nowrap',
          zIndex: 10,
        }}
      />
    </div>
  )
}

// ============================================================
// Vector Heatmap — canvas rendering of embedding values
// ============================================================
const VectorHeatmap = ({ embedding, dim }: { embedding: number[]; dim: number }) => {
  const canvasRef = useRef<HTMLCanvasElement>(null)

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas || !embedding.length) return
    const ctx = canvas.getContext('2d')
    if (!ctx) return

    const cols = Math.ceil(Math.sqrt(dim))
    const rows = Math.ceil(dim / cols)
    const cellSize = Math.max(2, Math.min(8, Math.floor(400 / cols)))

    canvas.width = cols * cellSize
    canvas.height = rows * cellSize

    const min = Math.min(...embedding)
    const max = Math.max(...embedding)
    const range = max - min || 1

    for (let i = 0; i < embedding.length; i++) {
      const col = i % cols
      const row = Math.floor(i / cols)
      const normalized = (embedding[i] - min) / range

      // Blue → White → Red color scale
      let r: number, g: number, b: number
      if (normalized < 0.5) {
        const t = normalized * 2
        r = Math.floor(t * 255)
        g = Math.floor(t * 255)
        b = 255
      } else {
        const t = (normalized - 0.5) * 2
        r = 255
        g = Math.floor((1 - t) * 255)
        b = Math.floor((1 - t) * 255)
      }

      ctx.fillStyle = `rgb(${r},${g},${b})`
      ctx.fillRect(col * cellSize, row * cellSize, cellSize, cellSize)
    }
  }, [embedding, dim])

  if (!embedding.length) return <Text type="secondary">无向量数据</Text>

  return (
    <div>
      <canvas
        ref={canvasRef}
        style={{
          borderRadius: 4,
          border: '1px solid #f0f0f0',
          maxWidth: '100%',
        }}
      />
      <div style={{ marginTop: 4, display: 'flex', alignItems: 'center', gap: 8 }}>
        <div
          style={{
            width: 80,
            height: 10,
            background: 'linear-gradient(to right, #0000ff, #ffffff, #ff0000)',
            borderRadius: 2,
          }}
        />
        <Text type="secondary" style={{ fontSize: 11 }}>
          蓝(负) → 白(零) → 红(正)
        </Text>
        <Text type="secondary" style={{ fontSize: 11 }}>
          维度: {dim}
        </Text>
      </div>
    </div>
  )
}

// ============================================================
// Main Component
// ============================================================
const DocumentObservabilityPanel = ({ documentId }: Props) => {
  const [loading, setLoading] = useState(false)
  const [data, setData] = useState<DocumentObservability | null>(null)

  useEffect(() => {
    if (!documentId) return
    setLoading(true)
    getDocumentObservability(documentId)
      .then(setData)
      .catch(() => message.error('获取文档可观测数据失败'))
      .finally(() => setLoading(false))
  }, [documentId])

  if (loading) return <Spin tip="加载中..." style={{ display: 'block', marginTop: 40 }} />
  if (!data) return <Empty description="暂无可观测数据" />

  const chunkCols: ColumnsType<ChunkDetail> = [
    { title: '#', dataIndex: 'chunk_index', width: 50 },
    {
      title: '类型',
      dataIndex: 'chunk_type',
      width: 90,
      render: (v: string) => <Tag>{v}</Tag>,
    },
    { title: '字数', dataIndex: 'word_count', width: 70 },
    { title: 'Vector ID', dataIndex: 'vector_id', width: 180, ellipsis: true },
    {
      title: '内容',
      dataIndex: 'content',
      render: (v: string) => (
        <Paragraph ellipsis={{ rows: 2, expandable: true, symbol: '展开' }}>
          {v}
        </Paragraph>
      ),
    },
  ]

  const vectorCols: ColumnsType<DocumentVectorSample> = [
    { title: 'Chunk', dataIndex: 'chunk_id', width: 200, ellipsis: true },
    { title: '维度', dataIndex: 'embedding_dim', width: 80 },
    {
      title: '内容片段',
      dataIndex: 'content',
      render: (v: string) => <Text>{(v || '').slice(0, 120)}</Text>,
    },
    {
      title: '向量热力图',
      key: 'heatmap',
      width: 200,
      render: (_, record) => (
        <VectorHeatmap embedding={record.embedding_full || []} dim={record.embedding_dim} />
      ),
    },
  ]

  const entityCols: ColumnsType<KBGraphEntitySample> = [
    { title: '实体名', dataIndex: 'name', width: 200 },
    {
      title: '类型',
      dataIndex: 'type',
      width: 120,
      render: (v: string) => <Tag color="blue">{v}</Tag>,
    },
    { title: '实体ID', dataIndex: 'entity_id', ellipsis: true },
  ]

  const relationCols: ColumnsType<KBGraphRelationSample> = [
    {
      title: '关系',
      key: 'relation',
      render: (_, row) => (
        <Space size={4}>
          <Tag>{row.source_type}</Tag>
          <Text strong>{row.source_name}</Text>
          <Tag color="blue">{row.relation_type}</Tag>
          <Text strong>{row.target_name}</Text>
          <Tag>{row.target_type}</Tag>
        </Space>
      ),
    },
    {
      title: '权重',
      dataIndex: 'weight',
      width: 80,
      render: (w: number) => Number(w || 0).toFixed(3),
    },
  ]

  const indexStatus = data.index_status
  const tokenStats = data.token_analysis?.stats

  return (
    <Space direction="vertical" style={{ width: '100%' }} size={16}>
      {/* Warnings */}
      {data.warnings && data.warnings.length > 0 && (
        <Alert
          type="warning"
          showIcon
          message="部分数据获取失败"
          description={data.warnings.join('；')}
        />
      )}

      {/* Index Status */}
      <Card title="索引落点" size="small">
        <Row gutter={16}>
          <Col span={6}>
            <Descriptions column={1} size="small">
              <Descriptions.Item label="状态">
                <Tag color={data.status === 'published' ? 'green' : 'orange'}>
                  {data.status}
                </Tag>
              </Descriptions.Item>
              <Descriptions.Item label="类型">
                <Tag>{data.doc_type || '未分类'}</Tag>
              </Descriptions.Item>
            </Descriptions>
          </Col>
          <Col span={4}>
            <Statistic
              title={
                <Space>
                  全文索引
                  {indexStatus?.in_text_index ? (
                    <Badge status="success" />
                  ) : (
                    <Badge status="error" />
                  )}
                </Space>
              }
              value={indexStatus?.text_index_count ?? 0}
              suffix="条"
              prefix={
                indexStatus?.in_text_index ? (
                  <CheckCircleOutlined style={{ color: '#52c41a' }} />
                ) : (
                  <CloseCircleOutlined style={{ color: '#ff4d4f' }} />
                )
              }
            />
          </Col>
          <Col span={4}>
            <Statistic
              title={
                <Space>
                  向量索引
                  {indexStatus?.in_vector_index ? (
                    <Badge status="success" />
                  ) : (
                    <Badge status="error" />
                  )}
                </Space>
              }
              value={indexStatus?.vector_index_count ?? 0}
              suffix="条"
              prefix={
                indexStatus?.in_vector_index ? (
                  <CheckCircleOutlined style={{ color: '#52c41a' }} />
                ) : (
                  <CloseCircleOutlined style={{ color: '#ff4d4f' }} />
                )
              }
            />
          </Col>
          <Col span={4}>
            <Statistic
              title="Chunk 数"
              value={data.chunks?.length ?? 0}
              prefix={<InfoCircleOutlined />}
            />
          </Col>
          <Col span={3}>
            <Statistic title="图实体" value={data.graph_entities?.length ?? 0} />
          </Col>
          <Col span={3}>
            <Statistic title="图关系" value={data.graph_relations?.length ?? 0} />
          </Col>
        </Row>
      </Card>

      {/* Tabbed detail views */}
      <Tabs
        defaultActiveKey="tokens"
        items={[
          {
            key: 'tokens',
            label: `分词结果 (${tokenStats?.total_tokens ?? 0})`,
            children: data.token_analysis ? (
              <Space direction="vertical" style={{ width: '100%' }} size={12}>
                <Card size="small" title="分词统计">
                  <Row gutter={16}>
                    <Col span={4}>
                      <Statistic
                        title="总词数"
                        value={tokenStats?.total_tokens ?? 0}
                      />
                    </Col>
                    <Col span={4}>
                      <Statistic
                        title="不重复词"
                        value={tokenStats?.unique_tokens ?? 0}
                      />
                    </Col>
                    <Col span={4}>
                      <Statistic
                        title="平均词长"
                        value={tokenStats?.avg_token_len?.toFixed(1) ?? '0'}
                      />
                    </Col>
                    <Col span={12}>
                      <div>
                        <Text type="secondary">词类分布：</Text>
                        <Space size={4} wrap style={{ marginTop: 4 }}>
                          {Object.entries(tokenStats?.token_types ?? {}).map(
                            ([type, count]) => (
                              <Tag key={type}>
                                {type}: {count}
                              </Tag>
                            )
                          )}
                        </Space>
                      </div>
                    </Col>
                  </Row>
                </Card>
                <Card size="small" title={`分词器: ${data.token_analysis.analyzer}`}>
                  <TokenCloud tokens={data.token_analysis.tokens} />
                </Card>
              </Space>
            ) : (
              <Empty description="无分词数据（索引未创建或分词失败）" />
            ),
          },
          {
            key: 'chunks',
            label: `Chunk 分块 (${data.chunks?.length ?? 0})`,
            children: (
              <Table
                rowKey="chunk_id"
                size="small"
                columns={chunkCols}
                dataSource={data.chunks || []}
                pagination={{ pageSize: 10, showSizeChanger: true }}
              />
            ),
          },
          {
            key: 'vectors',
            label: `向量 (${data.vector_samples?.length ?? 0})`,
            children:
              data.vector_samples?.length ? (
                <Table
                  rowKey="id"
                  size="small"
                  columns={vectorCols}
                  dataSource={data.vector_samples}
                  pagination={false}
                />
              ) : (
                <Empty description="无向量数据（索引未创建或 embedding 生成失败）" />
              ),
          },
          {
            key: 'graph',
            label: `知识图谱 (${data.graph_entities?.length ?? 0} 实体)`,
            children:
              data.graph_entities?.length ? (
                <Space direction="vertical" style={{ width: '100%' }} size={16}>
                  <GraphVisualization
                    entities={data.graph_entities}
                    relations={data.graph_relations || []}
                    width={780}
                    height={460}
                    title="文档知识图谱"
                  />
                  <Card size="small" title="实体列表">
                    <Table
                      rowKey="entity_id"
                      size="small"
                      columns={entityCols}
                      dataSource={data.graph_entities}
                      pagination={{ pageSize: 10 }}
                    />
                  </Card>
                  <Card size="small" title="关系列表">
                    <Table
                      rowKey="relation_id"
                      size="small"
                      columns={relationCols}
                      dataSource={data.graph_relations}
                      pagination={{ pageSize: 10 }}
                    />
                  </Card>
                </Space>
              ) : (
                <Empty description="无图谱数据" />
              ),
          },
          {
            key: 'llm_usage',
            label: (
              <span>
                <ThunderboltOutlined /> LLM 用量 ({data.llm_usage?.total_calls ?? 0} 次)
              </span>
            ),
            children: data.llm_usage && data.llm_usage.total_calls > 0 ? (
              <LLMUsagePanel usage={data.llm_usage} />
            ) : (
              <Empty description="暂无 LLM 用量记录（文档尚未处理或数据库中无记录）" />
            ),
          },
        ]}
      />
    </Space>
  )
}

export default DocumentObservabilityPanel

// ============================================================
// LLM Usage Panel — shows token consumption and cost
// ============================================================
const LLMUsagePanel = ({ usage }: { usage: NonNullable<DocumentObservability['llm_usage']> }) => {
  const methodCols: ColumnsType<MethodUsageStat> = [
    { title: '服务', dataIndex: 'caller_service', width: 100, render: (v: string) => <Tag color="blue">{v}</Tag> },
    { title: '方法', dataIndex: 'caller_method', width: 200 },
    { title: '调用次数', dataIndex: 'calls', width: 80, sorter: (a, b) => a.calls - b.calls },
    { title: '输入 Tokens', dataIndex: 'input_tokens', width: 110, render: (v: number) => v.toLocaleString() },
    { title: '输出 Tokens', dataIndex: 'output_tokens', width: 110, render: (v: number) => v.toLocaleString() },
    { title: '总 Tokens', dataIndex: 'total_tokens', width: 110, render: (v: number) => <Text strong>{v.toLocaleString()}</Text>, sorter: (a, b) => a.total_tokens - b.total_tokens, defaultSortOrder: 'descend' },
    { title: '成本 (USD)', dataIndex: 'cost_usd', width: 110, render: (v: number) => <Text type="success">${v.toFixed(6)}</Text> },
  ]

  const recordCols: ColumnsType<LLMUsageItem> = [
    { title: '时间', dataIndex: 'created_at', width: 170, render: (v: string) => new Date(v).toLocaleString() },
    { title: '服务', dataIndex: 'caller_service', width: 80, render: (v: string) => <Tag>{v}</Tag> },
    { title: '方法', dataIndex: 'caller_method', width: 180 },
    { title: '模型', dataIndex: 'model_id', width: 200, ellipsis: true, render: (v: string) => <Text code style={{ fontSize: 11 }}>{v}</Text> },
    { title: '类型', dataIndex: 'model_type', width: 80, render: (v: string) => <Tag color={v === 'chat' ? 'purple' : 'cyan'}>{v}</Tag> },
    { title: '输入', dataIndex: 'input_tokens', width: 80, render: (v: number) => v.toLocaleString() },
    { title: '输出', dataIndex: 'output_tokens', width: 80, render: (v: number) => v.toLocaleString() },
    { title: '总计', dataIndex: 'total_tokens', width: 80, render: (v: number) => <Text strong>{v.toLocaleString()}</Text> },
    { title: 'USD', dataIndex: 'cost_usd', width: 90, render: (v: number) => `$${v.toFixed(6)}` },
    { title: '状态', dataIndex: 'status', width: 70, render: (v: string) => <Tag color={v === 'success' ? 'green' : 'red'}>{v}</Tag> },
  ]

  // Calculate proportions for the progress bars
  const maxMethodTokens = Math.max(...(usage.by_method || []).map(m => m.total_tokens), 1)

  return (
    <Space direction="vertical" style={{ width: '100%' }} size={16}>
      {/* Summary Stats */}
      <Card size="small" title={<><DollarOutlined /> 用量汇总</>}>
        <Row gutter={16}>
          <Col span={4}>
            <Statistic title="LLM 调用次数" value={usage.total_calls} prefix={<ThunderboltOutlined />} />
          </Col>
          <Col span={5}>
            <Statistic title="输入 Tokens" value={usage.total_input_tokens} />
          </Col>
          <Col span={5}>
            <Statistic title="输出 Tokens" value={usage.total_output_tokens} />
          </Col>
          <Col span={5}>
            <Statistic title="总 Tokens" value={usage.total_tokens} valueStyle={{ color: '#1890ff', fontWeight: 'bold' }} />
          </Col>
          <Col span={5}>
            <Statistic
              title="估算成本"
              value={usage.estimated_cost_usd}
              precision={6}
              prefix="$"
              valueStyle={{ color: '#52c41a' }}
            />
          </Col>
        </Row>
      </Card>

      {/* By Method Breakdown */}
      {usage.by_method?.length > 0 && (
        <Card size="small" title="按方法分布">
          <Space direction="vertical" style={{ width: '100%' }} size={8}>
            {usage.by_method.map((m, i) => (
              <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <Tag color="blue" style={{ minWidth: 70, textAlign: 'center' }}>{m.caller_service}</Tag>
                <Text style={{ minWidth: 180 }}>{m.caller_method}</Text>
                <Progress
                  percent={Math.round((m.total_tokens / maxMethodTokens) * 100)}
                  size="small"
                  style={{ flex: 1, margin: 0 }}
                  format={() => `${m.total_tokens.toLocaleString()} tokens`}
                />
                <Text type="secondary" style={{ minWidth: 90, textAlign: 'right' }}>
                  ${m.cost_usd.toFixed(6)}
                </Text>
              </div>
            ))}
          </Space>
        </Card>
      )}

      {/* By Model Type */}
      {usage.by_model_type?.length > 0 && (
        <Card size="small" title="按模型分布">
          <Row gutter={16}>
            {usage.by_model_type.map((m, i) => (
              <Col key={i} span={12}>
                <Descriptions column={2} size="small" bordered>
                  <Descriptions.Item label="模型">
                    <Text code>{m.model_id}</Text>
                  </Descriptions.Item>
                  <Descriptions.Item label="类型">
                    <Tag color={m.model_type === 'chat' ? 'purple' : 'cyan'}>{m.model_type}</Tag>
                  </Descriptions.Item>
                  <Descriptions.Item label="调用次数">{m.calls}</Descriptions.Item>
                  <Descriptions.Item label="总 Tokens">{m.total_tokens.toLocaleString()}</Descriptions.Item>
                </Descriptions>
              </Col>
            ))}
          </Row>
        </Card>
      )}

      {/* Detailed method table */}
      <Card size="small" title="方法级明细">
        <Table
          rowKey={(_, i) => String(i)}
          size="small"
          columns={methodCols}
          dataSource={usage.by_method || []}
          pagination={false}
          scroll={{ x: true }}
        />
      </Card>

      {/* All records */}
      <Card size="small" title="调用记录明细">
        <Table
          rowKey="id"
          size="small"
          columns={recordCols}
          dataSource={usage.records || []}
          pagination={{ pageSize: 10, showSizeChanger: true }}
          scroll={{ x: true }}
        />
      </Card>
    </Space>
  )
}
