import { useEffect, useState } from 'react'
import {
  Button,
  Card,
  Input,
  Popconfirm,
  Select,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
  Upload,
  message,
} from 'antd'
import { UploadOutlined } from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { Document, KnowledgeBase } from '@/types'
import { deleteDocument, getDocuments, getKnowledgeBases, processDocument, uploadDocument } from '@/api/knowledge'

const { Title, Text } = Typography
const { Search } = Input

const DocumentListPage = () => {
  const [documents, setDocuments] = useState<Document[]>([])
  const [loading, setLoading] = useState(false)
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(10)
  const [keyword, setKeyword] = useState('')
  const [knowledgeBases, setKnowledgeBases] = useState<KnowledgeBase[]>([])
  const [selectedKnowledgeBaseId, setSelectedKnowledgeBaseId] = useState<string>()
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()

  useEffect(() => {
    void fetchKnowledgeBases()
  }, [])

  useEffect(() => {
    void fetchDocuments()
  }, [page, pageSize, keyword, selectedKnowledgeBaseId])

  const fetchKnowledgeBases = async () => {
    try {
      const kbs = await getKnowledgeBases()
      setKnowledgeBases(kbs)
      // Prioritize kb from URL query param (?kb=xxx)
      const kbFromUrl = searchParams.get('kb')
      if (kbFromUrl && kbs.some(k => k.id === kbFromUrl)) {
        setSelectedKnowledgeBaseId(kbFromUrl)
      } else if (!selectedKnowledgeBaseId && kbs.length > 0) {
        setSelectedKnowledgeBaseId(kbs[0].id)
      }
    } catch {
      message.error('获取知识库列表失败')
    }
  }

  const fetchDocuments = async () => {
    if (!selectedKnowledgeBaseId) {
      setDocuments([])
      setTotal(0)
      return
    }

    setLoading(true)
    try {
      const result = await getDocuments({
        knowledge_base_id: selectedKnowledgeBaseId,
        keyword,
        page,
        page_size: pageSize,
      })
      setDocuments(result.data || [])
      setTotal(result.total || 0)
    } catch {
      message.error('获取文档列表失败')
    } finally {
      setLoading(false)
    }
  }

  const handleUpload = async (file: File) => {
    if (!selectedKnowledgeBaseId) {
      message.error('请先选择知识库后再上传')
      return false
    }

    try {
      const doc = await uploadDocument(file, { knowledge_base_id: selectedKnowledgeBaseId })
      message.success('上传成功，系统将自动识别类型')
      await processDocument(doc.id)
      message.info('已开始构建索引，正在跳转到处理详情')
      void fetchDocuments()
      navigate(`/documents/${doc.id}`)
    } catch {
      message.error('上传失败')
    }
    return false
  }

  const handleDelete = async (id: string) => {
    try {
      await deleteDocument(id)
      message.success('删除成功')
      void fetchDocuments()
    } catch {
      message.error('删除失败')
    }
  }

  const handleProcess = async (id: string) => {
    try {
      await processDocument(id)
      message.success('开始处理文档')
      navigate(`/documents/${id}`)
    } catch {
      message.error('处理失败')
    }
  }

  const columns: ColumnsType<Document> = [
    {
      title: '标题',
      dataIndex: 'title',
      key: 'title',
      render: (text, record) => <a onClick={() => navigate(`/documents/${record.id}`)}>{text}</a>,
    },
    {
      title: '自动识别类型',
      dataIndex: 'doc_type',
      key: 'doc_type',
      width: 140,
      render: (type: string) => {
        const typeColors: Record<string, string> = {
          knowledge: 'blue',
          process: 'green',
          data: 'orange',
          brief: 'purple',
          experience: 'cyan',
        }
        return <Tag color={typeColors[type] || 'default'}>{type || 'unknown'}</Tag>
      },
    },
    {
      title: '格式',
      dataIndex: 'format',
      key: 'format',
      width: 100,
    },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      width: 120,
      render: (status: string) => {
        const statusColors: Record<string, string> = {
          draft: 'default',
          processing: 'processing',
          published: 'success',
          failed: 'error',
          archived: 'warning',
        }
        return <Tag color={statusColors[status] || 'default'}>{status}</Tag>
      },
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      key: 'created_at',
      width: 180,
      render: (date: string) => new Date(date).toLocaleString(),
    },
    {
      title: '操作',
      key: 'action',
      width: 220,
      render: (_, record) => (
        <Space>
          <Button type="link" size="small" onClick={() => navigate(`/documents/${record.id}`)}>
            查看过程
          </Button>
          <Button type="link" size="small" onClick={() => handleProcess(record.id)}>
            重新处理
          </Button>
          <Popconfirm title="确定删除此文档？" onConfirm={() => void handleDelete(record.id)}>
            <Button type="link" danger size="small">
              删除
            </Button>
          </Popconfirm>
        </Space>
      ),
    },
  ]

  return (
    <div>
      <div style={{ marginBottom: 24, display: 'flex', justifyContent: 'space-between', gap: 12, flexWrap: 'wrap' }}>
        <div>
          <Title level={4} style={{ margin: 0 }}>文档管理</Title>
          <Text type="secondary" style={{ fontSize: 13 }}>上传文档，系统自动识别类型并构建索引</Text>
        </div>
        <Space>
          <Select
            placeholder="选择知识库"
            style={{ width: 260 }}
            value={selectedKnowledgeBaseId}
            onChange={(value) => {
              setSelectedKnowledgeBaseId(value)
              setPage(1)
            }}
            options={knowledgeBases.map((kb) => ({ label: kb.name, value: kb.id }))}
          />
          <Upload
            beforeUpload={handleUpload}
            showUploadList={false}
            accept=".md,.markdown,.txt,.html,.htm,.docx,.doc,.xlsx,.xls,.csv"
          >
            <Tooltip title="支持 Markdown、TXT、HTML、DOCX、XLSX、CSV">
              <Button icon={<UploadOutlined />} disabled={!selectedKnowledgeBaseId}>
                上传文档
              </Button>
            </Tooltip>
          </Upload>
        </Space>
      </div>

      <Card>
        <div style={{ marginBottom: 16 }}>
          <Search
            placeholder="搜索文档标题或内容"
            allowClear
            onSearch={(value) => {
              setKeyword(value)
              setPage(1)
            }}
            style={{ width: 320 }}
            disabled={!selectedKnowledgeBaseId}
          />
        </div>

        <Table
          columns={columns}
          dataSource={documents}
          rowKey="id"
          loading={loading}
          locale={
            selectedKnowledgeBaseId
              ? { emptyText: '当前知识库暂无文档' }
              : { emptyText: '请先选择知识库' }
          }
          pagination={{
            current: page,
            pageSize,
            total,
            showSizeChanger: true,
            showTotal: (t) => `共 ${t} 条`,
            onChange: (p, ps) => {
              setPage(p)
              setPageSize(ps)
            },
          }}
        />
      </Card>
    </div>
  )
}

export default DocumentListPage
