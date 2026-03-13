import { useEffect, useState } from 'react'
import { Button, Card, Col, Empty, Popconfirm, Row, Spin, Typography, message } from 'antd'
import {
  PlusOutlined,
  BookOutlined,
  DeleteOutlined,
  FileTextOutlined,
  EyeOutlined,
} from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { KnowledgeBase } from '@/types'
import { getKnowledgeBases, createKnowledgeBase, deleteKnowledgeBase } from '@/api/knowledge'
import CreateKBModal from '@/components/CreateKBModal'

const { Title, Text, Paragraph } = Typography

// Gradient icon backgrounds for visual variety
const cardGradients = [
  'linear-gradient(135deg, #667eea 0%, #764ba2 100%)',
  'linear-gradient(135deg, #f093fb 0%, #f5576c 100%)',
  'linear-gradient(135deg, #4facfe 0%, #00f2fe 100%)',
  'linear-gradient(135deg, #43e97b 0%, #38f9d7 100%)',
  'linear-gradient(135deg, #fa709a 0%, #fee140 100%)',
  'linear-gradient(135deg, #a18cd1 0%, #fbc2eb 100%)',
]

const KnowledgeBasePage = () => {
  const [knowledgeBases, setKnowledgeBases] = useState<KnowledgeBase[]>([])
  const [loading, setLoading] = useState(false)
  const [modalVisible, setModalVisible] = useState(false)
  const navigate = useNavigate()

  useEffect(() => {
    fetchKnowledgeBases()
  }, [])

  const fetchKnowledgeBases = async () => {
    setLoading(true)
    try {
      const kbs = await getKnowledgeBases()
      setKnowledgeBases(kbs)
    } catch {
      message.error('获取知识库列表失败')
    } finally {
      setLoading(false)
    }
  }

  const handleCreate = async (data: { name: string; description: string }) => {
    try {
      await createKnowledgeBase(data)
      message.success('创建知识库成功')
      setModalVisible(false)
      fetchKnowledgeBases()
    } catch {
      message.error('创建知识库失败')
    }
  }

  const handleDelete = async (id: string) => {
    try {
      await deleteKnowledgeBase(id)
      message.success('删除知识库成功')
      fetchKnowledgeBases()
    } catch {
      message.error('删除知识库失败')
    }
  }

  return (
    <div>
      <div style={{ marginBottom: 28, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <div>
          <Title level={4} style={{ margin: 0 }}>知识库</Title>
          <Text type="secondary" style={{ fontSize: 13 }}>
            管理你的知识库，上传文档并构建索引
          </Text>
        </div>
        <Button
          type="primary"
          icon={<PlusOutlined />}
          onClick={() => setModalVisible(true)}
          style={{
            borderRadius: 8,
            height: 38,
            background: '#4f46e5',
            borderColor: '#4f46e5',
          }}
        >
          新建知识库
        </Button>
      </div>

      {loading ? (
        <div style={{ textAlign: 'center', padding: 80 }}>
          <Spin size="large" />
        </div>
      ) : knowledgeBases.length === 0 ? (
        <div style={{ textAlign: 'center', padding: '80px 0' }}>
          <Empty
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            description={
              <div>
                <Text type="secondary" style={{ fontSize: 14 }}>暂无知识库</Text>
                <br />
                <Button
                  type="link"
                  onClick={() => setModalVisible(true)}
                  style={{ padding: 0, marginTop: 8 }}
                >
                  创建你的第一个知识库 →
                </Button>
              </div>
            }
          />
        </div>
      ) : (
        <Row gutter={[20, 20]}>
          {knowledgeBases.map((kb, index) => (
            <Col xs={24} sm={12} lg={8} key={kb.id}>
              <Card
                hoverable
                onClick={() => navigate(`/documents?kb=${kb.id}`)}
                style={{
                  borderRadius: 12,
                  border: '1px solid #e2e8f0',
                  overflow: 'hidden',
                  transition: 'all 0.2s ease',
                }}
                bodyStyle={{ padding: 0 }}
              >
                {/* Gradient header strip */}
                <div
                  style={{
                    height: 6,
                    background: cardGradients[index % cardGradients.length],
                  }}
                />
                <div style={{ padding: '20px 20px 16px' }}>
                  <div style={{ display: 'flex', alignItems: 'flex-start', gap: 14 }}>
                    {/* Icon */}
                    <div
                      style={{
                        width: 44,
                        height: 44,
                        borderRadius: 10,
                        background: cardGradients[index % cardGradients.length],
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                        flexShrink: 0,
                      }}
                    >
                      <BookOutlined style={{ fontSize: 20, color: '#fff' }} />
                    </div>
                    <div style={{ flex: 1, minWidth: 0 }}>
                      <Text strong style={{ fontSize: 15, display: 'block' }}>
                        {kb.name}
                      </Text>
                      <Paragraph
                        type="secondary"
                        ellipsis={{ rows: 2 }}
                        style={{ fontSize: 13, margin: '4px 0 0' }}
                      >
                        {kb.description || '暂无描述'}
                      </Paragraph>
                    </div>
                  </div>
                </div>
                {/* Action bar */}
                <div
                  style={{
                    padding: '10px 20px',
                    borderTop: '1px solid #f1f5f9',
                    display: 'flex',
                    justifyContent: 'space-between',
                    background: '#fafbfc',
                  }}
                >
                  <div style={{ display: 'flex', gap: 4 }}>
                    <Button
                      type="text"
                      size="small"
                      icon={<FileTextOutlined />}
                      onClick={(e) => {
                        e.stopPropagation()
                        navigate(`/documents?kb=${kb.id}`)
                      }}
                      style={{ fontSize: 12, color: '#64748b' }}
                    >
                      文档
                    </Button>
                    <Button
                      type="text"
                      size="small"
                      icon={<EyeOutlined />}
                      onClick={(e) => {
                        e.stopPropagation()
                        navigate(`/observability?kb=${kb.id}`)
                      }}
                      style={{ fontSize: 12, color: '#64748b' }}
                    >
                      可观测
                    </Button>
                  </div>
                  <Popconfirm
                    title="确定删除此知识库？"
                    description="删除后知识库下的所有文档和索引将被清除"
                    onConfirm={() => handleDelete(kb.id)}
                    onCancel={(e) => e?.stopPropagation()}
                  >
                    <Button
                      type="text"
                      danger
                      size="small"
                      icon={<DeleteOutlined />}
                      onClick={(e) => e.stopPropagation()}
                      style={{ fontSize: 12 }}
                    />
                  </Popconfirm>
                </div>
              </Card>
            </Col>
          ))}
        </Row>
      )}

      <CreateKBModal
        visible={modalVisible}
        onCancel={() => setModalVisible(false)}
        onSubmit={handleCreate}
      />
    </div>
  )
}

export default KnowledgeBasePage
