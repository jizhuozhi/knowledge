import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import {
  Button,
  Card,
  Col,
  Descriptions,
  Divider,
  Empty,
  Progress,
  Row,
  Space,
  Spin,
  Statistic,
  Steps,
  Tabs,
  Tag,
  Timeline,
  Typography,
  message,
} from 'antd'
import {
  ArrowLeftOutlined,
  ReloadOutlined,
  EyeOutlined,
  FileTextOutlined,
  ScissorOutlined,
  ThunderboltOutlined,
  DatabaseOutlined,
  ApartmentOutlined,
  BulbOutlined,
  CheckCircleOutlined,
  CloseCircleOutlined,
  WarningOutlined,
  DeleteOutlined,
  TagOutlined,
  ExperimentOutlined,
  RocketOutlined,
} from '@ant-design/icons'
import { Document, ProcessingEvent } from '@/types'
import { getDocument, getDocumentProcessingEvents, processDocument } from '@/api/knowledge'
import DocumentObservabilityPanel from '@/components/DocumentObservabilityPanel'

const { Title, Text } = Typography

const DocumentDetailPage = () => {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [document, setDocument] = useState<Document | null>(null)
  const [events, setEvents] = useState<ProcessingEvent[]>([])
  const [loading, setLoading] = useState(false)
  const [eventLoading, setEventLoading] = useState(false)

  const isProcessing = useMemo(() => {
    if (!document) return false
    return ['draft', 'processing'].includes(document.status)
  }, [document])

  const fetchDocument = useCallback(async (silent = false) => {
    if (!id) return
    if (!silent) setLoading(true)
    try {
      const doc = await getDocument(id)
      setDocument(doc)
    } catch {
      message.error('获取文档详情失败')
    } finally {
      if (!silent) setLoading(false)
    }
  }, [id])

  const fetchEvents = useCallback(async (silent = false) => {
    if (!id) return
    if (!silent) setEventLoading(true)
    try {
      const result = await getDocumentProcessingEvents(id)
      setEvents(result.data || [])
    } catch {
      // 允许静默失败，避免轮询打扰
    } finally {
      if (!silent) setEventLoading(false)
    }
  }, [id])

  useEffect(() => {
    if (!id) return
    void fetchDocument()
    void fetchEvents()
  }, [id, fetchDocument, fetchEvents])

  useEffect(() => {
    if (!id || !isProcessing) return
    const timer = window.setInterval(() => {
      void fetchDocument(true)
      void fetchEvents(true)
    }, 2500)
    return () => window.clearInterval(timer)
  }, [id, isProcessing, fetchDocument, fetchEvents])

  const handleProcess = async () => {
    try {
      await processDocument(id!)
      message.success('开始重新处理文档')
      void fetchDocument(true)
      void fetchEvents(true)
    } catch {
      message.error('处理失败')
    }
  }

  if (loading) {
    return (
      <div style={{ textAlign: 'center', padding: 50 }}>
        <Spin size="large" />
      </div>
    )
  }

  if (!document) {
    return <Empty description="文档不存在" />
  }

  const statusColor: Record<string, string> = {
    draft: 'default',
    processing: 'processing',
    published: 'success',
    failed: 'error',
    archived: 'warning',
  }

  const eventColor = (status: ProcessingEvent['status']) => {
    switch (status) {
      case 'success':
        return 'green'
      case 'warning':
        return 'orange'
      case 'failed':
        return 'red'
      default:
        return 'blue'
    }
  }

  // ====== Stage display config ======
  const stageConfig: Record<string, { icon: React.ReactNode; label: string; color: string }> = {
    start:             { icon: <RocketOutlined />,        label: '开始处理',     color: '#1890ff' },
    cleanup:           { icon: <DeleteOutlined />,        label: '清理旧索引',   color: '#8c8c8c' },
    doc_type:          { icon: <TagOutlined />,           label: '文档类型识别', color: '#722ed1' },
    features:          { icon: <ExperimentOutlined />,    label: '特征抽取',     color: '#13c2c2' },
    strategy:          { icon: <BulbOutlined />,          label: '策略推理',     color: '#eb2f96' },
    semantic_metadata: { icon: <FileTextOutlined />,      label: '语义元数据',   color: '#fa8c16' },
    chunk:             { icon: <ScissorOutlined />,       label: '文档分块',     color: '#2f54eb' },
    embedding:         { icon: <ThunderboltOutlined />,   label: '向量生成',     color: '#52c41a' },
    index:             { icon: <DatabaseOutlined />,      label: '索引写入',     color: '#1890ff' },
    graph:             { icon: <ApartmentOutlined />,     label: '图谱构建',     color: '#722ed1' },
    summary:           { icon: <FileTextOutlined />,      label: 'AI 摘要',     color: '#fa8c16' },
    finish:            { icon: <CheckCircleOutlined />,   label: '构建完成',     color: '#52c41a' },
    failed:            { icon: <CloseCircleOutlined />,   label: '构建失败',     color: '#ff4d4f' },
  }

  const getStageInfo = (stage: string) => stageConfig[stage] || { icon: <FileTextOutlined />, label: stage, color: '#8c8c8c' }

  // ====== Business-specific detail renderers ======
  const renderEventDetails = (event: ProcessingEvent) => {
    if (!event.details || Object.keys(event.details).length === 0) return null
    const d = event.details

    switch (event.stage) {
      case 'start':
        return (
          <Descriptions size="small" column={3} bordered style={{ marginTop: 8 }}>
            <Descriptions.Item label="文档标题">{String(d.document_title || '-')}</Descriptions.Item>
            <Descriptions.Item label="格式"><Tag>{String(d.format || '-')}</Tag></Descriptions.Item>
            <Descriptions.Item label="文档类型"><Tag color="blue">{String(d.doc_type || '-')}</Tag></Descriptions.Item>
          </Descriptions>
        )

      case 'cleanup':
        if (d.error) {
          return (
            <div style={{ marginTop: 8, padding: '8px 12px', background: '#fffbe6', borderRadius: 6, border: '1px solid #ffe58f' }}>
              <WarningOutlined style={{ color: '#faad14', marginRight: 6 }} />
              <Text type="warning">{String(d.error)}</Text>
            </div>
          )
        }
        return null

      case 'doc_type':
        return (
          <div style={{ marginTop: 8, padding: '10px 14px', background: '#f9f0ff', borderRadius: 6, border: '1px solid #d3adf7' }}>
            <Space direction="vertical" size={4}>
              <Space>
                <Text strong>识别结果：</Text>
                <Tag color="purple" style={{ fontSize: 14 }}>{String(d.doc_type || '-')}</Tag>
              </Space>
              <Text type="secondary" style={{ fontSize: 12 }}>
                <BulbOutlined style={{ marginRight: 4 }} />
                推理依据：{String(d.reason || '-')}
              </Text>
            </Space>
          </div>
        )

      case 'features':
        return (
          <div style={{ marginTop: 8 }}>
            <Row gutter={[12, 8]}>
              <Col span={6}>
                <Card size="small" bodyStyle={{ padding: '8px 12px' }}>
                  <Statistic title="内容长度" value={Number(d.content_length) || 0} suffix="字符" valueStyle={{ fontSize: 16 }} />
                </Card>
              </Col>
              <Col span={6}>
                <Card size="small" bodyStyle={{ padding: '8px 12px' }}>
                  <Statistic title="行数" value={Number(d.line_count) || 0} suffix="行" valueStyle={{ fontSize: 16 }} />
                </Card>
              </Col>
              <Col span={12}>
                <Card size="small" bodyStyle={{ padding: '8px 12px' }}>
                  <Text strong style={{ fontSize: 12, color: '#8c8c8c', display: 'block', marginBottom: 4 }}>文档特征</Text>
                  <Space wrap size={4}>
                    <Tag color={d.has_code_blocks ? 'blue' : 'default'} icon={d.has_code_blocks ? <CheckCircleOutlined /> : undefined}>
                      代码块
                    </Tag>
                    <Tag color={d.has_tables ? 'blue' : 'default'} icon={d.has_tables ? <CheckCircleOutlined /> : undefined}>
                      表格
                    </Tag>
                    <Tag color={d.has_steps ? 'blue' : 'default'} icon={d.has_steps ? <CheckCircleOutlined /> : undefined}>
                      步骤
                    </Tag>
                    <Tag color={d.has_sections ? 'blue' : 'default'} icon={d.has_sections ? <CheckCircleOutlined /> : undefined}>
                      多章节 ({Number(d.section_count_hint) || 0})
                    </Tag>
                  </Space>
                </Card>
              </Col>
            </Row>
          </div>
        )

      case 'strategy':
        return (
          <div style={{ marginTop: 8, padding: '12px 14px', background: '#fff0f6', borderRadius: 6, border: '1px solid #ffadd2' }}>
            <Row gutter={16}>
              <Col span={6}>
                <Text type="secondary" style={{ fontSize: 11 }}>分块策略</Text>
                <div><Tag color="magenta" style={{ fontSize: 13, marginTop: 2 }}>{String(d.chunk_strategy || '-')}</Tag></div>
              </Col>
              <Col span={4}>
                <Text type="secondary" style={{ fontSize: 11 }}>分块大小</Text>
                <div><Text strong style={{ fontSize: 15 }}>{Number(d.chunk_size) || '-'}</Text></div>
              </Col>
              <Col span={4}>
                <Text type="secondary" style={{ fontSize: 11 }}>图谱索引</Text>
                <div>
                  {d.enable_graph_index
                    ? <Tag color="green" icon={<CheckCircleOutlined />}>启用</Tag>
                    : <Tag color="default">禁用</Tag>
                  }
                </div>
              </Col>
              <Col span={4}>
                <Text type="secondary" style={{ fontSize: 11 }}>AI 摘要</Text>
                <div>
                  {d.enable_ai_summary
                    ? <Tag color="green" icon={<CheckCircleOutlined />}>启用</Tag>
                    : <Tag color="default">禁用</Tag>
                  }
                </div>
              </Col>
              <Col span={6}>
                <Text type="secondary" style={{ fontSize: 11 }}>特殊处理</Text>
                <div>
                  {d.special_processing
                    ? <Tag color="orange">{String(d.special_processing)}</Tag>
                    : <Text type="secondary">-</Text>
                  }
                </div>
              </Col>
            </Row>
          </div>
        )

      case 'semantic_metadata':
        if (d.error) {
          return (
            <div style={{ marginTop: 8, padding: '8px 12px', background: '#fffbe6', borderRadius: 6, border: '1px solid #ffe58f' }}>
              <WarningOutlined style={{ color: '#faad14', marginRight: 6 }} />
              <Text type="warning">{String(d.error)}</Text>
            </div>
          )
        }
        // eslint-disable-next-line no-case-declarations
        const metadata = d.semantic_metadata as Record<string, unknown> | undefined
        return (
          <div style={{ marginTop: 8 }}>
            <Descriptions size="small" column={2} bordered>
              <Descriptions.Item label="提取字段数">
                <Tag color="blue">{Number(d.field_count) || 0} 个字段</Tag>
              </Descriptions.Item>
            </Descriptions>
            {metadata && Object.keys(metadata).length > 0 && (
              <div style={{ marginTop: 8, background: '#f6ffed', padding: '10px 12px', borderRadius: 6, border: '1px solid #b7eb8f' }}>
                <Text strong style={{ fontSize: 12, marginBottom: 6, display: 'block' }}>语义元数据</Text>
                <Space direction="vertical" size={2} style={{ width: '100%' }}>
                  {Object.entries(metadata).map(([key, val]) => (
                    <div key={key} style={{ display: 'flex', gap: 8 }}>
                      <Tag style={{ minWidth: 80 }}>{key}</Tag>
                      <Text ellipsis style={{ flex: 1, fontSize: 12 }}>{typeof val === 'object' ? JSON.stringify(val) : String(val)}</Text>
                    </div>
                  ))}
                </Space>
              </div>
            )}
          </div>
        )

      case 'chunk':
        // eslint-disable-next-line no-case-declarations
        const samples = Array.isArray(d.samples) ? d.samples as Array<Record<string, unknown>> : []
        return (
          <div style={{ marginTop: 8 }}>
            <Row gutter={12}>
              <Col span={6}>
                <Card size="small" bodyStyle={{ padding: '8px 12px' }}>
                  <Statistic title="总分块数" value={Number(d.chunk_count) || 0} valueStyle={{ fontSize: 18, color: '#1890ff' }} />
                </Card>
              </Col>
              <Col span={6}>
                <Card size="small" bodyStyle={{ padding: '8px 12px' }}>
                  <Statistic title="分块策略" value={String(d.chunk_strategy || '-')} valueStyle={{ fontSize: 14 }} />
                </Card>
              </Col>
              <Col span={6}>
                <Card size="small" bodyStyle={{ padding: '8px 12px' }}>
                  <Statistic title="分块大小" value={Number(d.chunk_size) || 0} suffix="字符" valueStyle={{ fontSize: 14 }} />
                </Card>
              </Col>
            </Row>
            {samples.length > 0 && (
              <div style={{ marginTop: 8 }}>
                <Text strong style={{ fontSize: 12 }}>分块样例（前 {samples.length} 段）</Text>
                <div style={{ marginTop: 6 }}>
                  {samples.map((s, i) => (
                    <div key={i} style={{ marginBottom: 8, background: '#fafafa', padding: '8px 12px', borderRadius: 6, borderLeft: '3px solid #1890ff' }}>
                      <Space size={8} style={{ marginBottom: 4 }}>
                        <Tag>#{Number(s.chunk_index ?? i)}</Tag>
                        <Tag color="blue">{String(s.chunk_type || 'text')}</Tag>
                        <Text type="secondary" style={{ fontSize: 11 }}>{Number(s.length) || 0} 字符</Text>
                      </Space>
                      <div style={{ fontSize: 12, color: '#595959', lineHeight: 1.6 }}>
                        {String(s.preview || '').slice(0, 200)}{String(s.preview || '').length > 200 ? '...' : ''}
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        )

      case 'embedding':
        return (
          <Row gutter={12} style={{ marginTop: 8 }}>
            <Col span={8}>
              <Card size="small" bodyStyle={{ padding: '8px 12px' }}>
                <Statistic
                  title="生成向量数"
                  value={Number(d.embedding_count) || 0}
                  prefix={<ThunderboltOutlined style={{ color: '#52c41a' }} />}
                  valueStyle={{ fontSize: 18, color: '#52c41a' }}
                />
              </Card>
            </Col>
            <Col span={8}>
              <Card size="small" bodyStyle={{ padding: '8px 12px' }}>
                <Statistic
                  title="Embedding Tokens"
                  value={Number(d.embedding_tokens) || 0}
                  valueStyle={{ fontSize: 14 }}
                />
              </Card>
            </Col>
          </Row>
        )

      case 'index':
        return (
          <Row gutter={12} style={{ marginTop: 8 }}>
            <Col span={8}>
              <Card size="small" bodyStyle={{ padding: '8px 12px', background: '#e6f7ff' }}>
                <Statistic title="Chunk 索引" value={Number(d.indexed_chunks) || 0} suffix="条" prefix={<DatabaseOutlined />} valueStyle={{ fontSize: 16 }} />
              </Card>
            </Col>
            <Col span={8}>
              <Card size="small" bodyStyle={{ padding: '8px 12px', background: '#f6ffed' }}>
                <Statistic title="Section 索引" value={Number(d.indexed_sections) || 0} suffix="条" prefix={<DatabaseOutlined />} valueStyle={{ fontSize: 16 }} />
              </Card>
            </Col>
          </Row>
        )

      case 'graph':
        if (d.error) {
          return (
            <div style={{ marginTop: 8, padding: '8px 12px', background: '#fffbe6', borderRadius: 6, border: '1px solid #ffe58f' }}>
              <WarningOutlined style={{ color: '#faad14', marginRight: 6 }} />
              <Text type="warning">{String(d.error)}</Text>
            </div>
          )
        }
        return null

      case 'summary':
        if (d.error) {
          return (
            <div style={{ marginTop: 8, padding: '8px 12px', background: '#fffbe6', borderRadius: 6, border: '1px solid #ffe58f' }}>
              <WarningOutlined style={{ color: '#faad14', marginRight: 6 }} />
              <Text type="warning">{String(d.error)}</Text>
            </div>
          )
        }
        return (
          <div style={{ marginTop: 8 }}>
            <Descriptions size="small" column={1} bordered>
              <Descriptions.Item label="摘要长度">
                <Tag color="blue">{Number(d.summary_length) || 0} 字符</Tag>
              </Descriptions.Item>
            </Descriptions>
            {typeof d.summary_preview === 'string' && (
              <div style={{ marginTop: 8, background: '#f6ffed', padding: '12px 14px', borderRadius: 6, border: '1px solid #b7eb8f' }}>
                <Text strong style={{ fontSize: 12, display: 'block', marginBottom: 6 }}>
                  <BulbOutlined style={{ marginRight: 4 }} />AI 摘要预览
                </Text>
                <div style={{ fontSize: 13, lineHeight: 1.8, color: '#262626' }}>{d.summary_preview}</div>
              </div>
            )}
          </div>
        )

      case 'finish':
        return (
          <div style={{ marginTop: 8, padding: '10px 14px', background: '#f6ffed', borderRadius: 6, border: '1px solid #b7eb8f' }}>
            <CheckCircleOutlined style={{ color: '#52c41a', fontSize: 16, marginRight: 8 }} />
            <Text strong style={{ color: '#52c41a' }}>索引构建完成，文档状态: {String(d.status || 'published')}</Text>
          </div>
        )

      case 'failed':
        return null // The message itself already shows the error

      default:
        // Fallback: render raw JSON for unknown stages
        return (
          <pre style={{ background: '#fafafa', marginTop: 8, padding: 10, borderRadius: 6, whiteSpace: 'pre-wrap', maxHeight: 200, overflow: 'auto', fontSize: 11 }}>
            {JSON.stringify(d, null, 2)}
          </pre>
        )
    }
  }

  // Compute pipeline progress
  const pipelineStages = ['start', 'cleanup', 'doc_type', 'features', 'strategy', 'semantic_metadata', 'chunk', 'embedding', 'index', 'graph', 'summary', 'finish']
  const completedStages = new Set(events.filter(e => e.status === 'success' || e.status === 'warning').map(e => e.stage))
  const failedStages = new Set(events.filter(e => e.status === 'failed').map(e => e.stage))
  const hasFailed = failedStages.size > 0
  const progressPercent = Math.round((completedStages.size / pipelineStages.length) * 100)

  return (
    <div>
      <div style={{ marginBottom: 24, display: 'flex', justifyContent: 'space-between' }}>
        <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/documents')}>
          返回列表
        </Button>
        <Space>
          <Button type="primary" icon={<ReloadOutlined />} onClick={handleProcess}>
            重新处理
          </Button>
        </Space>
      </div>

      <Card>
        <Descriptions title="文档信息" bordered column={2} size="small">
          <Descriptions.Item label="标题">{document.title}</Descriptions.Item>
          <Descriptions.Item label="自动识别类型">
            <Tag color="blue">{document.doc_type}</Tag>
          </Descriptions.Item>
          <Descriptions.Item label="格式">{document.format}</Descriptions.Item>
          <Descriptions.Item label="状态">
            <Tag color={statusColor[document.status] || 'default'}>{document.status}</Tag>
          </Descriptions.Item>
          <Descriptions.Item label="创建时间">{new Date(document.created_at).toLocaleString()}</Descriptions.Item>
          <Descriptions.Item label="更新时间">{new Date(document.updated_at).toLocaleString()}</Descriptions.Item>
          {document.summary && (
            <Descriptions.Item label="摘要" span={2}>
              {document.summary}
            </Descriptions.Item>
          )}
        </Descriptions>
      </Card>

      <Card style={{ marginTop: 16 }}>
        <Tabs
          defaultActiveKey="observability"
          items={[
            {
              key: 'observability',
              label: (
                <span>
                  <EyeOutlined /> 可观测性
                </span>
              ),
              children: <DocumentObservabilityPanel documentId={id!} />,
            },
            {
              key: 'process',
              label: '索引构建过程',
              children: (
                <div>
                  <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 16 }}>
                    <Title level={5} style={{ margin: 0 }}>
                      索引构建与决策过程
                    </Title>
                    <Space>
                      {isProcessing && <Text type="secondary">处理中，自动刷新中...</Text>}
                      <Button size="small" icon={<ReloadOutlined />} onClick={() => void fetchEvents()} loading={eventLoading}>
                        刷新
                      </Button>
                    </Space>
                  </div>

                  {events.length === 0 ? (
                    <Empty description="暂无处理过程记录，请先触发处理" />
                  ) : (
                    <Space direction="vertical" style={{ width: '100%' }} size={16}>
                      {/* Overall progress */}
                      <Card size="small" bodyStyle={{ padding: '12px 16px' }}>
                        <Row gutter={16} align="middle">
                          <Col span={4}>
                            <Progress
                              type="circle"
                              percent={hasFailed ? progressPercent : progressPercent}
                              size={56}
                              status={hasFailed ? 'exception' : isProcessing ? 'active' : 'success'}
                            />
                          </Col>
                          <Col span={20}>
                            <Steps
                              size="small"
                              current={pipelineStages.findIndex(s => !completedStages.has(s) && !failedStages.has(s))}
                              status={hasFailed ? 'error' : isProcessing ? 'process' : 'finish'}
                              items={pipelineStages
                                .filter(s => completedStages.has(s) || failedStages.has(s) || s === pipelineStages[0])
                                .map(s => {
                                  const info = getStageInfo(s)
                                  return {
                                    title: <span style={{ fontSize: 11 }}>{info.label}</span>,
                                    icon: failedStages.has(s) ? <CloseCircleOutlined style={{ color: '#ff4d4f' }} /> : undefined,
                                    status: failedStages.has(s) ? 'error' as const : completedStages.has(s) ? 'finish' as const : 'wait' as const,
                                  }
                                })}
                            />
                          </Col>
                        </Row>
                      </Card>

                      {/* Detailed timeline */}
                      <Timeline
                        items={events.map((event) => {
                          const info = getStageInfo(event.stage)
                          return {
                            color: eventColor(event.status),
                            dot: <span style={{ color: info.color }}>{info.icon}</span>,
                            children: (
                              <div>
                                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                                  <Space size={8}>
                                    <Tag style={{ margin: 0 }}>#{event.step}</Tag>
                                    <Text strong style={{ color: info.color }}>{info.label}</Text>
                                    <Tag
                                      color={eventColor(event.status)}
                                      icon={
                                        event.status === 'success' ? <CheckCircleOutlined /> :
                                        event.status === 'failed' ? <CloseCircleOutlined /> :
                                        event.status === 'warning' ? <WarningOutlined /> :
                                        undefined
                                      }
                                    >
                                      {event.status}
                                    </Tag>
                                  </Space>
                                  <Text type="secondary" style={{ fontSize: 11 }}>
                                    {new Date(event.created_at).toLocaleString()}
                                  </Text>
                                </div>
                                <div style={{ marginTop: 4, color: '#595959' }}>{event.message}</div>
                                {renderEventDetails(event)}
                              </div>
                            ),
                          }
                        })}
                      />
                    </Space>
                  )}
                </div>
              ),
            },
            {
              key: 'content',
              label: '文档内容',
              children: (
                <div>
                  <div
                    style={{
                      background: '#f5f5f5',
                      padding: 16,
                      borderRadius: 8,
                      maxHeight: 480,
                      overflow: 'auto',
                    }}
                  >
                    <pre style={{ margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>{document.content}</pre>
                  </div>

                  {document.semantic_metadata && Object.keys(document.semantic_metadata).length > 0 && (
                    <>
                      <Divider />
                      <Title level={5}>语义元数据</Title>
                      <pre style={{ background: '#f5f5f5', padding: 16, borderRadius: 8 }}>
                        {JSON.stringify(document.semantic_metadata, null, 2)}
                      </pre>
                    </>
                  )}
                </div>
              ),
            },
          ]}
        />
      </Card>
    </div>
  )
}

export default DocumentDetailPage
