import { useState, useEffect } from 'react'
import { Card, Form, Input, Button, Typography, Space, message, List, Tag, Divider } from 'antd'
import { PlusOutlined, RightOutlined, CopyOutlined, TeamOutlined } from '@ant-design/icons'
import { useAuthStore } from '@/stores/authStore'
import { createTenant, listTenants } from '@/api/knowledge'

const { Title, Text, Paragraph } = Typography

interface Tenant {
  id: string
  name: string
  code: string
  description: string
  status: string
  created_at: string
}

const TenantSettingsPage = () => {
  const [loading, setLoading] = useState(false)
  const [tenants, setTenants] = useState<Tenant[]>([])
  const [tenantsLoading, setTenantsLoading] = useState(false)
  const [showCreateForm, setShowCreateForm] = useState(false)
  const { tenantId, setTenant } = useAuthStore()

  const isInLayout = !!tenantId

  useEffect(() => {
    loadTenants()
  }, [])

  const loadTenants = async () => {
    setTenantsLoading(true)
    try {
      const result = await listTenants()
      setTenants(result.data || [])
    } catch {
      // 忽略错误
    } finally {
      setTenantsLoading(false)
    }
  }

  const handleCreateTenant = async (values: { name: string; code: string; description?: string }) => {
    setLoading(true)
    try {
      const tenant = await createTenant(values)
      setTenant(tenant.id)
      message.success('租户创建成功，已自动进入')
      loadTenants()
      setShowCreateForm(false)
    } catch {
      message.error('创建租户失败')
    } finally {
      setLoading(false)
    }
  }

  const handleSelectTenant = (id: string) => {
    setTenant(id)
    message.success('已切换租户')
  }

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text)
    message.success('已复制')
  }

  // Full-page welcome (not in layout)
  if (!isInLayout) {
    return (
      <div
        style={{
          minHeight: '100vh',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          background: 'linear-gradient(135deg, #1e1b4b 0%, #312e81 50%, #4338ca 100%)',
          padding: 24,
        }}
      >
        <Card
          style={{
            width: 480,
            maxWidth: '100%',
            borderRadius: 16,
            boxShadow: '0 20px 60px rgba(0,0,0,0.3)',
            border: 'none',
          }}
          bodyStyle={{ padding: 32 }}
        >
          {/* Logo + Welcome */}
          <div style={{ textAlign: 'center', marginBottom: 28 }}>
            <div
              style={{
                width: 56,
                height: 56,
                borderRadius: 14,
                background: 'linear-gradient(135deg, #818cf8, #6366f1)',
                display: 'inline-flex',
                alignItems: 'center',
                justifyContent: 'center',
                fontSize: 24,
                fontWeight: 700,
                color: '#fff',
                marginBottom: 16,
              }}
            >
              K
            </div>
            <Title level={3} style={{ margin: 0 }}>
              Knowledge RAG
            </Title>
            <Paragraph type="secondary" style={{ margin: '8px 0 0' }}>
              智能知识库检索增强生成平台
            </Paragraph>
          </div>

          {/* Tenant list */}
          {tenants.length > 0 && (
            <>
              <Text strong style={{ fontSize: 13, color: '#64748b', textTransform: 'uppercase' as const, letterSpacing: '0.05em' }}>
                选择租户
              </Text>
              <List
                style={{ marginTop: 8, marginBottom: 20 }}
                dataSource={tenants}
                loading={tenantsLoading}
                renderItem={(tenant) => (
                  <div
                    key={tenant.id}
                    onClick={() => handleSelectTenant(tenant.id)}
                    style={{
                      padding: '12px 14px',
                      borderRadius: 10,
                      border: '1px solid #e2e8f0',
                      marginBottom: 8,
                      cursor: 'pointer',
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'space-between',
                      transition: 'all 0.15s ease',
                    }}
                    onMouseEnter={(e) => {
                      e.currentTarget.style.borderColor = '#818cf8'
                      e.currentTarget.style.background = '#f5f3ff'
                    }}
                    onMouseLeave={(e) => {
                      e.currentTarget.style.borderColor = '#e2e8f0'
                      e.currentTarget.style.background = '#fff'
                    }}
                  >
                    <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                      <TeamOutlined style={{ color: '#6366f1', fontSize: 16 }} />
                      <div>
                        <Text strong style={{ fontSize: 14 }}>{tenant.name}</Text>
                        <div>
                          <Tag style={{ margin: 0, fontSize: 11 }}>{tenant.code}</Tag>
                        </div>
                      </div>
                    </div>
                    <RightOutlined style={{ color: '#94a3b8', fontSize: 12 }} />
                  </div>
                )}
              />
              <Divider style={{ margin: '16px 0' }}>
                <Text type="secondary" style={{ fontSize: 12 }}>或</Text>
              </Divider>
            </>
          )}

          {/* Create new tenant */}
          {showCreateForm ? (
            <Form layout="vertical" onFinish={handleCreateTenant}>
              <Form.Item name="name" label="租户名称" rules={[{ required: true, message: '请输入租户名称' }]}>
                <Input placeholder="例如：研发团队" style={{ borderRadius: 8 }} />
              </Form.Item>
              <Form.Item name="code" label="租户代码" rules={[{ required: true, message: '请输入租户代码' }]}>
                <Input placeholder="例如：dev-team" style={{ borderRadius: 8 }} />
              </Form.Item>
              <Form.Item name="description" label="描述（可选）">
                <Input placeholder="租户描述" style={{ borderRadius: 8 }} />
              </Form.Item>
              <Space style={{ width: '100%' }}>
                <Button onClick={() => setShowCreateForm(false)}>取消</Button>
                <Button
                  type="primary"
                  htmlType="submit"
                  loading={loading}
                  style={{ background: '#4f46e5', borderColor: '#4f46e5', borderRadius: 8 }}
                >
                  创建并进入
                </Button>
              </Space>
            </Form>
          ) : (
            <Button
              block
              icon={<PlusOutlined />}
              onClick={() => setShowCreateForm(true)}
              style={{
                height: 44,
                borderRadius: 10,
                borderStyle: 'dashed',
                color: '#6366f1',
                borderColor: '#c7d2fe',
              }}
            >
              创建新租户
            </Button>
          )}
        </Card>
      </div>
    )
  }

  // In-layout settings page
  return (
    <div>
      <div style={{ marginBottom: 24 }}>
        <Title level={4} style={{ margin: 0 }}>设置</Title>
        <Text type="secondary">管理租户信息</Text>
      </div>

      <Card title="当前租户" style={{ borderRadius: 12, marginBottom: 20 }}>
        <Space>
          <Text>ID:</Text>
          <Text code>{tenantId}</Text>
          <Button
            size="small"
            icon={<CopyOutlined />}
            onClick={() => copyToClipboard(tenantId)}
          >
            复制
          </Button>
        </Space>
      </Card>

      {tenants.length > 0 && (
        <Card title="所有租户" style={{ borderRadius: 12, marginBottom: 20 }}>
          <List
            dataSource={tenants}
            loading={tenantsLoading}
            renderItem={(tenant) => (
              <List.Item
                actions={[
                  tenant.id === tenantId ? (
                    <Tag color="green" key="current">当前</Tag>
                  ) : (
                    <Button
                      type="link"
                      size="small"
                      key="select"
                      onClick={() => handleSelectTenant(tenant.id)}
                    >
                      切换
                    </Button>
                  ),
                ]}
              >
                <List.Item.Meta
                  title={
                    <Space>
                      {tenant.name}
                      <Tag>{tenant.code}</Tag>
                    </Space>
                  }
                  description={<Text type="secondary" style={{ fontSize: 12 }}>ID: {tenant.id}</Text>}
                />
              </List.Item>
            )}
          />
        </Card>
      )}

      <Card title="创建新租户" style={{ borderRadius: 12 }}>
        <Form layout="vertical" onFinish={handleCreateTenant} style={{ maxWidth: 400 }}>
          <Form.Item name="name" label="租户名称" rules={[{ required: true, message: '请输入租户名称' }]}>
            <Input placeholder="例如：研发团队" />
          </Form.Item>
          <Form.Item name="code" label="租户代码" rules={[{ required: true, message: '请输入租户代码' }]}>
            <Input placeholder="例如：dev-team" />
          </Form.Item>
          <Form.Item name="description" label="描述（可选）">
            <Input placeholder="租户描述" />
          </Form.Item>
          <Button
            type="primary"
            htmlType="submit"
            loading={loading}
            style={{ background: '#4f46e5', borderColor: '#4f46e5', borderRadius: 8 }}
          >
            创建租户
          </Button>
        </Form>
      </Card>
    </div>
  )
}

export default TenantSettingsPage
