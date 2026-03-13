import { ReactNode } from 'react'
import { Layout, Menu, Typography, Space, Tag } from 'antd'
import {
  BookOutlined,
  FileTextOutlined,
  SearchOutlined,
  FundProjectionScreenOutlined,
  SettingOutlined,
} from '@ant-design/icons'
import { useNavigate, useLocation } from 'react-router-dom'
import { useAuthStore } from '@/stores/authStore'

const { Header, Sider, Content } = Layout
const { Text } = Typography

interface MainLayoutProps {
  children: ReactNode
}

const MainLayout = ({ children }: MainLayoutProps) => {
  const navigate = useNavigate()
  const location = useLocation()
  const { tenantId, logout } = useAuthStore()

  const menuItems = [
    { key: '/knowledge', icon: <BookOutlined />, label: '知识库' },
    { key: '/documents', icon: <FileTextOutlined />, label: '文档管理' },
    { key: '/rag', icon: <SearchOutlined />, label: '智能检索' },
    { key: '/observability', icon: <FundProjectionScreenOutlined />, label: '可观测' },
    { key: '/settings', icon: <SettingOutlined />, label: '设置' },
  ]

  const getSelectedKey = () => {
    const path = location.pathname
    if (path.startsWith('/documents')) return '/documents'
    if (path.startsWith('/knowledge')) return '/knowledge'
    if (path.startsWith('/rag')) return '/rag'
    if (path.startsWith('/observability')) return '/observability'
    if (path.startsWith('/settings')) return '/settings'
    return path
  }

  return (
    <Layout style={{ height: '100vh' }}>
      {/* Dark gradient sidebar */}
      <Sider
        width={220}
        style={{
          background: 'linear-gradient(180deg, #1e1b4b 0%, #312e81 100%)',
          overflow: 'auto',
        }}
      >
        {/* Logo area */}
        <div
          style={{
            height: 64,
            display: 'flex',
            alignItems: 'center',
            padding: '0 20px',
            gap: 10,
          }}
        >
          <div
            style={{
              width: 32,
              height: 32,
              borderRadius: 8,
              background: 'linear-gradient(135deg, #818cf8, #6366f1)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              fontSize: 16,
              fontWeight: 700,
              color: '#fff',
              flexShrink: 0,
            }}
          >
            K
          </div>
          <Text
            style={{
              color: '#fff',
              fontSize: 15,
              fontWeight: 600,
              letterSpacing: '-0.02em',
              whiteSpace: 'nowrap',
            }}
          >
            Knowledge RAG
          </Text>
        </div>

        <Menu
          mode="inline"
          className="sidebar-menu"
          selectedKeys={[getSelectedKey()]}
          items={menuItems}
          onClick={({ key }) => navigate(key)}
          style={{ marginTop: 8 }}
        />

        {/* Bottom tenant info */}
        <div
          style={{
            position: 'absolute',
            bottom: 0,
            left: 0,
            right: 0,
            padding: '12px 16px',
            borderTop: '1px solid rgba(255,255,255,0.08)',
          }}
        >
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <Text style={{ color: 'rgba(255,255,255,0.45)', fontSize: 11 }}>
              租户 {tenantId?.slice(0, 6) ?? ''}…
            </Text>
            <Text
              style={{ color: 'rgba(255,255,255,0.45)', fontSize: 11, cursor: 'pointer' }}
              onClick={logout}
            >
              切换
            </Text>
          </div>
        </div>
      </Sider>

      <Layout>
        {/* Slim top bar */}
        <Header
          style={{
            height: 52,
            lineHeight: '52px',
            background: '#fff',
            borderBottom: '1px solid var(--color-border, #e2e8f0)',
            padding: '0 24px',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
          }}
        >
          <Space size={12}>
            <Text strong style={{ fontSize: 15, color: 'var(--color-text)' }}>
              {menuItems.find((m) => m.key === getSelectedKey())?.label || ''}
            </Text>
          </Space>
          <Tag color="purple" style={{ margin: 0 }}>
            v0.1.0-beta
          </Tag>
        </Header>

        {/* Main content */}
        <Content
          style={{
            padding: 20,
            overflow: 'auto',
            background: 'var(--color-bg, #f8fafc)',
          }}
        >
          <div
            style={{
              background: '#fff',
              borderRadius: 12,
              padding: 24,
              minHeight: '100%',
              boxShadow: '0 1px 3px rgba(0,0,0,0.04)',
            }}
          >
            {children}
          </div>
        </Content>
      </Layout>
    </Layout>
  )
}

export default MainLayout
