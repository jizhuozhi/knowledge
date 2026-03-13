import { useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import {
  Alert,
  Button,
  Card,
  Col,
  Descriptions,
  Empty,
  InputNumber,
  Progress,
  Row,
  Select,
  Space,
  Statistic,
  Table,
  Tabs,
  Tag,
  Typography,
  message,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import {
  DatabaseOutlined,
  DollarOutlined,
  NodeIndexOutlined,
  SearchOutlined,
  ApartmentOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons'
import {
  getKnowledgeBaseObservability,
  getKnowledgeBases,
} from '@/api/knowledge'
import {
  KnowledgeBase,
  KnowledgeBaseObservability,
  KBChunkSample,
  KBTextIndexSample,
  KBVectorIndexSample,
  KBGraphRelationSample,
  KBGraphEntitySample,
  ServiceUsageStat,
  DocUsageStat,
} from '@/types'
import GraphVisualization from '@/components/GraphVisualization'

const { Title, Text, Paragraph } = Typography

const KnowledgeObservabilityPage = () => {
  const [loading, setLoading] = useState(false)
  const [kbOptions, setKbOptions] = useState<KnowledgeBase[]>([])
  const [selectedKbId, setSelectedKbId] = useState<string>()
  const [limit, setLimit] = useState(20)
  const [data, setData] = useState<KnowledgeBaseObservability | null>(null)
  const [searchParams] = useSearchParams()

  useEffect(() => {
    void init()
  }, [])

  const init = async () => {
    try {
      const kbs = await getKnowledgeBases()
      setKbOptions(kbs)
      // Prioritize kb from URL query param (?kb=xxx)
      const kbFromUrl = searchParams.get('kb')
      if (kbFromUrl && kbs.some(k => k.id === kbFromUrl)) {
        setSelectedKbId(kbFromUrl)
      } else if (kbs.length > 0) {
        setSelectedKbId(kbs[0].id)
      }
    } catch {
      message.error('获取知识库列表失败')
    }
  }

  const loadData = async () => {
    if (!selectedKbId) {
      message.error('请先选择知识库')
      return
    }

    setLoading(true)
    try {
      const result = await getKnowledgeBaseObservability(selectedKbId, limit)
      setData(result)
    } catch {
      message.error('获取可观测数据失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    if (selectedKbId) {
      void loadData()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedKbId])

  const textCols: ColumnsType<KBTextIndexSample> = [
    { title: '文档ID', dataIndex: 'document_id', width: 200, ellipsis: true },
    { title: '标题', dataIndex: 'title', width: 180, ellipsis: true },
    {
      title: '内容片段',
      dataIndex: 'content',
      render: (v: string) => (
        <Paragraph ellipsis={{ rows: 2, expandable: true, symbol: '展开' }}>
          {v || ''}
        </Paragraph>
      ),
    },
    {
      title: '类型',
      dataIndex: 'doc_type',
      width: 100,
      render: (v: string) => <Tag>{v}</Tag>,
    },
  ]

  const vectorCols: ColumnsType<KBVectorIndexSample> = [
    { title: '文档ID', dataIndex: 'document_id', width: 200, ellipsis: true },
    { title: '标题', dataIndex: 'title', width: 180, ellipsis: true },
    {
      title: '维度',
      dataIndex: 'embedding_dim',
      width: 80,
      render: (v: number) => <Tag color="blue">{v}d</Tag>,
    },
    {
      title: '向量预览(前8维)',
      dataIndex: 'embedding_preview',
      render: (arr: number[]) => (
        <Text code style={{ fontSize: 11 }}>
          [{Array.isArray(arr) ? arr.map((v) => v.toFixed(4)).join(', ') : ''}]
        </Text>
      ),
    },
  ]

  const graphCols: ColumnsType<KBGraphRelationSample> = [
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
    { title: '源文档', dataIndex: 'source_document_id', width: 180, ellipsis: true },
    { title: '目标文档', dataIndex: 'target_document_id', width: 180, ellipsis: true },
  ]

  const entityCols: ColumnsType<KBGraphEntitySample> = [
    { title: '实体名', dataIndex: 'name', width: 200 },
    {
      title: '类型',
      dataIndex: 'type',
      width: 120,
      render: (v: string) => <Tag color="blue">{v}</Tag>,
    },
    { title: '文档ID', dataIndex: 'document_id', ellipsis: true },
    { title: '实体ID', dataIndex: 'entity_id', ellipsis: true },
  ]

  const chunkCols: ColumnsType<KBChunkSample> = [
    { title: '文档ID', dataIndex: 'document_id', width: 200, ellipsis: true },
    { title: '#', dataIndex: 'chunk_index', width: 60 },
    {
      title: '类型',
      dataIndex: 'chunk_type',
      width: 90,
      render: (v: string) => <Tag>{v}</Tag>,
    },
    { title: 'Vector ID', dataIndex: 'vector_id', width: 180, ellipsis: true },
    {
      title: 'Chunk内容',
      dataIndex: 'content',
      render: (v: string) => (
        <Paragraph ellipsis={{ rows: 2, expandable: true, symbol: '展开' }}>
          {v || ''}
        </Paragraph>
      ),
    },
  ]

  const stats = useMemo(() => data?.stats || {}, [data])

  return (
    <div>
      <div style={{ marginBottom: 16 }}>
        <Title level={4} style={{ margin: 0 }}>
          KB 可观测
        </Title>
      </div>

      <Card style={{ marginBottom: 16 }}>
        <Space wrap>
          <Select
            style={{ width: 320 }}
            placeholder="选择知识库"
            value={selectedKbId}
            onChange={setSelectedKbId}
            options={kbOptions.map((kb) => ({ label: kb.name, value: kb.id }))}
          />
          <InputNumber
            min={1}
            max={100}
            value={limit}
            onChange={(v) => setLimit(typeof v === 'number' ? v : 20)}
            addonBefore="样本上限"
          />
          <Button type="primary" loading={loading} onClick={loadData}>
            刷新
          </Button>
        </Space>
      </Card>

      {data?.warnings && data.warnings.length > 0 && (
        <Alert
          type="warning"
          showIcon
          message="部分数据源读取失败"
          description={data.warnings.join('；')}
          style={{ marginBottom: 16 }}
        />
      )}

      <Row gutter={12} style={{ marginBottom: 16 }}>
        <Col span={3}>
          <Card size="small">
            <Statistic title="文档" value={stats.documents || 0} />
          </Card>
        </Col>
        <Col span={3}>
          <Card size="small">
            <Statistic title="Chunk" value={stats.chunks || 0} />
          </Card>
        </Col>
        <Col span={3}>
          <Card size="small">
            <Statistic title="图实体" value={stats.graph_entities || 0} />
          </Card>
        </Col>
        <Col span={3}>
          <Card size="small">
            <Statistic title="图关系" value={stats.graph_relations || 0} />
          </Card>
        </Col>
        <Col span={3}>
          <Card size="small">
            <Statistic title="全文索引" value={stats.text_index_samples || 0} />
          </Card>
        </Col>
        <Col span={3}>
          <Card size="small">
            <Statistic title="向量索引" value={stats.vector_index_samples || 0} />
          </Card>
        </Col>
        <Col span={3}>
          <Card size="small">
            <Statistic title="LLM 调用" value={data?.llm_usage?.total_calls || 0} prefix={<ThunderboltOutlined />} valueStyle={{ color: '#722ed1' }} />
          </Card>
        </Col>
        <Col span={3}>
          <Card size="small">
            <Statistic title="LLM 成本" value={data?.llm_usage?.estimated_cost_usd || 0} precision={4} prefix="$" valueStyle={{ color: '#52c41a' }} />
          </Card>
        </Col>
      </Row>

      {!data ? (
        <Empty description="请选择知识库并加载数据" />
      ) : (
        <Tabs
          defaultActiveKey="graph"
          size="large"
          items={[
            {
              key: 'graph',
              label: (
                <span>
                  <ApartmentOutlined /> 知识图谱
                </span>
              ),
              children: (
                <Space direction="vertical" style={{ width: '100%' }} size={16}>
                  <GraphVisualization
                    entities={data.graph_entity_samples || []}
                    relations={data.graph_relation_samples || []}
                    width={900}
                    height={520}
                    title="全局知识图谱"
                  />
                  <Row gutter={16}>
                    <Col span={12}>
                      <Card size="small" title="实体列表">
                        <Table
                          rowKey="entity_id"
                          size="small"
                          loading={loading}
                          columns={entityCols}
                          dataSource={data.graph_entity_samples || []}
                          pagination={{ pageSize: 10, showSizeChanger: true }}
                          scroll={{ x: true }}
                        />
                      </Card>
                    </Col>
                    <Col span={12}>
                      <Card size="small" title="关系列表">
                        <Table
                          rowKey="relation_id"
                          size="small"
                          loading={loading}
                          columns={graphCols}
                          dataSource={data.graph_relation_samples || []}
                          pagination={{ pageSize: 10, showSizeChanger: true }}
                          scroll={{ x: true }}
                        />
                      </Card>
                    </Col>
                  </Row>
                </Space>
              ),
            },
            {
              key: 'chunks',
              label: (
                <span>
                  <DatabaseOutlined /> Chunk 分块
                </span>
              ),
              children: (
                <Card title="解析后 Chunk（结构化分块）">
                  <Table
                    rowKey="chunk_id"
                    size="small"
                    loading={loading}
                    columns={chunkCols}
                    dataSource={data.chunk_samples || []}
                    pagination={{ pageSize: 20, showSizeChanger: true }}
                    scroll={{ x: true }}
                  />
                </Card>
              ),
            },
            {
              key: 'text',
              label: (
                <span>
                  <SearchOutlined /> 全文索引
                </span>
              ),
              children: (
                <Card title="全文检索索引（OpenSearch Text）">
                  <Table
                    rowKey="id"
                    size="small"
                    loading={loading}
                    columns={textCols}
                    dataSource={data.text_index_samples || []}
                    pagination={{ pageSize: 20, showSizeChanger: true }}
                    scroll={{ x: true }}
                  />
                </Card>
              ),
            },
            {
              key: 'vector',
              label: (
                <span>
                  <NodeIndexOutlined /> 向量索引
                </span>
              ),
              children: (
                <Card title="向量检索索引（OpenSearch Vector）">
                  <Table
                    rowKey="id"
                    size="small"
                    loading={loading}
                    columns={vectorCols}
                    dataSource={data.vector_index_samples || []}
                    pagination={{ pageSize: 20, showSizeChanger: true }}
                    scroll={{ x: true }}
                  />
                </Card>
              ),
            },
            {
              key: 'llm_usage',
              label: (
                <span>
                  <DollarOutlined /> LLM 用量
                </span>
              ),
              children: data.llm_usage ? (
                <KBLLMUsagePanel usage={data.llm_usage} loading={loading} />
              ) : (
                <Empty description="暂无 LLM 用量数据" />
              ),
            },
          ]}
        />
      )}
    </div>
  )
}

// ============================================================
// KB-level LLM Usage Panel
// ============================================================
const KBLLMUsagePanel = ({
  usage,
  loading,
}: {
  usage: NonNullable<KnowledgeBaseObservability['llm_usage']>
  loading: boolean
}) => {
  const serviceCols: ColumnsType<ServiceUsageStat> = [
    {
      title: '服务',
      dataIndex: 'caller_service',
      width: 140,
      render: (v: string) => <Tag color="blue">{v}</Tag>,
    },
    {
      title: '调用次数',
      dataIndex: 'calls',
      width: 100,
      sorter: (a, b) => a.calls - b.calls,
    },
    {
      title: '输入 Tokens',
      dataIndex: 'input_tokens',
      width: 120,
      render: (v: number) => v.toLocaleString(),
    },
    {
      title: '输出 Tokens',
      dataIndex: 'output_tokens',
      width: 120,
      render: (v: number) => v.toLocaleString(),
    },
    {
      title: '总 Tokens',
      dataIndex: 'total_tokens',
      width: 120,
      render: (v: number) => <Text strong>{v.toLocaleString()}</Text>,
      sorter: (a, b) => a.total_tokens - b.total_tokens,
      defaultSortOrder: 'descend',
    },
    {
      title: '成本 (USD)',
      dataIndex: 'cost_usd',
      width: 120,
      render: (v: number) => <Text type="success">${v.toFixed(6)}</Text>,
      sorter: (a, b) => a.cost_usd - b.cost_usd,
    },
  ]

  const docCols: ColumnsType<DocUsageStat> = [
    {
      title: '文档 ID',
      dataIndex: 'document_id',
      width: 220,
      ellipsis: true,
      render: (v: string) => <Text code style={{ fontSize: 11 }}>{v}</Text>,
    },
    {
      title: '文档标题',
      dataIndex: 'document_title',
      width: 200,
      ellipsis: true,
    },
    {
      title: '调用次数',
      dataIndex: 'calls',
      width: 100,
      sorter: (a, b) => a.calls - b.calls,
    },
    {
      title: '总 Tokens',
      dataIndex: 'total_tokens',
      width: 120,
      render: (v: number) => <Text strong>{v.toLocaleString()}</Text>,
      sorter: (a, b) => a.total_tokens - b.total_tokens,
      defaultSortOrder: 'descend',
    },
    {
      title: '成本 (USD)',
      dataIndex: 'cost_usd',
      width: 120,
      render: (v: number) => <Text type="success">${v.toFixed(6)}</Text>,
      sorter: (a, b) => a.cost_usd - b.cost_usd,
    },
  ]

  const maxServiceTokens = Math.max(
    ...(usage.by_service || []).map((s) => s.total_tokens),
    1,
  )

  return (
    <Space direction="vertical" style={{ width: '100%' }} size={16}>
      {/* Summary Stats */}
      <Card size="small" title={<><DollarOutlined /> 用量汇总</>}>
        <Row gutter={16}>
          <Col span={4}>
            <Statistic
              title="LLM 调用次数"
              value={usage.total_calls}
              prefix={<ThunderboltOutlined />}
            />
          </Col>
          <Col span={5}>
            <Statistic title="输入 Tokens" value={usage.total_input_tokens} />
          </Col>
          <Col span={5}>
            <Statistic title="输出 Tokens" value={usage.total_output_tokens} />
          </Col>
          <Col span={5}>
            <Statistic
              title="总 Tokens"
              value={usage.total_tokens}
              valueStyle={{ color: '#1890ff', fontWeight: 'bold' }}
            />
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

      {/* By Service Breakdown */}
      {usage.by_service?.length > 0 && (
        <Card size="small" title="按服务分布">
          <Space direction="vertical" style={{ width: '100%' }} size={8}>
            {usage.by_service.map((s, i) => (
              <div
                key={i}
                style={{ display: 'flex', alignItems: 'center', gap: 8 }}
              >
                <Tag color="blue" style={{ minWidth: 100, textAlign: 'center' }}>
                  {s.caller_service}
                </Tag>
                <Text style={{ minWidth: 70 }}>{s.calls} 次</Text>
                <Progress
                  percent={Math.round(
                    (s.total_tokens / maxServiceTokens) * 100,
                  )}
                  size="small"
                  style={{ flex: 1, margin: 0 }}
                  format={() => `${s.total_tokens.toLocaleString()} tokens`}
                />
                <Text
                  type="secondary"
                  style={{ minWidth: 90, textAlign: 'right' }}
                >
                  ${s.cost_usd.toFixed(6)}
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
              <Col key={i} span={12} style={{ marginBottom: 12 }}>
                <Descriptions column={2} size="small" bordered>
                  <Descriptions.Item label="模型">
                    <Text code>{m.model_id}</Text>
                  </Descriptions.Item>
                  <Descriptions.Item label="类型">
                    <Tag color={m.model_type === 'chat' ? 'purple' : 'cyan'}>
                      {m.model_type}
                    </Tag>
                  </Descriptions.Item>
                  <Descriptions.Item label="调用次数">
                    {m.calls}
                  </Descriptions.Item>
                  <Descriptions.Item label="总 Tokens">
                    {m.total_tokens.toLocaleString()}
                  </Descriptions.Item>
                  <Descriptions.Item label="输入 / 输出">
                    {m.input_tokens.toLocaleString()} /{' '}
                    {m.output_tokens.toLocaleString()}
                  </Descriptions.Item>
                  <Descriptions.Item label="成本">
                    <Text type="success">${m.cost_usd.toFixed(6)}</Text>
                  </Descriptions.Item>
                </Descriptions>
              </Col>
            ))}
          </Row>
        </Card>
      )}

      {/* Service Detail Table */}
      <Card size="small" title="服务级明细">
        <Table
          rowKey={(_, i) => String(i)}
          size="small"
          loading={loading}
          columns={serviceCols}
          dataSource={usage.by_service || []}
          pagination={false}
          scroll={{ x: true }}
        />
      </Card>

      {/* Top Documents Table */}
      {usage.top_documents?.length > 0 && (
        <Card size="small" title="Top 文档（按 Token 消耗排序）">
          <Table
            rowKey="document_id"
            size="small"
            loading={loading}
            columns={docCols}
            dataSource={usage.top_documents}
            pagination={{ pageSize: 10, showSizeChanger: true }}
            scroll={{ x: true }}
          />
        </Card>
      )}
    </Space>
  )
}

export default KnowledgeObservabilityPage
