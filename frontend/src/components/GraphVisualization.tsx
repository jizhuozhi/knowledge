import { useEffect, useRef, useState, useCallback } from 'react'
import { Card, Empty, Space, Tag, Typography, Tooltip } from 'antd'
import type { KBGraphEntitySample, KBGraphRelationSample } from '@/types'

const { Text } = Typography

interface GraphNode {
  id: string
  name: string
  type: string
  x: number
  y: number
  vx: number
  vy: number
  radius: number
  color: string
}

interface GraphEdge {
  source: string
  target: string
  type: string
  weight: number
}

interface GraphVisualizationProps {
  entities: KBGraphEntitySample[]
  relations: KBGraphRelationSample[]
  width?: number
  height?: number
  title?: string
}

const TYPE_COLORS: Record<string, string> = {
  person: '#1890ff',
  concept: '#52c41a',
  service: '#722ed1',
  component: '#fa8c16',
  api: '#eb2f96',
  process: '#13c2c2',
  technology: '#faad14',
  organization: '#2f54eb',
  default: '#8c8c8c',
}

function getColor(type: string): string {
  return TYPE_COLORS[type.toLowerCase()] || TYPE_COLORS.default
}

const GraphVisualization = ({
  entities,
  relations,
  width = 800,
  height = 500,
  title = '知识图谱可视化',
}: GraphVisualizationProps) => {
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const animRef = useRef<number>(0)
  const [hoveredNode, setHoveredNode] = useState<GraphNode | null>(null)
  const [tooltipPos, setTooltipPos] = useState({ x: 0, y: 0 })
  const nodesRef = useRef<GraphNode[]>([])
  const edgesRef = useRef<GraphEdge[]>([])
  const dragRef = useRef<{ node: GraphNode | null; offsetX: number; offsetY: number }>({
    node: null,
    offsetX: 0,
    offsetY: 0,
  })

  // Build graph data
  useEffect(() => {
    if (!entities.length) return

    const nodeMap = new Map<string, GraphNode>()
    entities.forEach((e) => {
      if (!nodeMap.has(e.entity_id)) {
        nodeMap.set(e.entity_id, {
          id: e.entity_id,
          name: e.name,
          type: e.type,
          x: width / 2 + (Math.random() - 0.5) * width * 0.6,
          y: height / 2 + (Math.random() - 0.5) * height * 0.6,
          vx: 0,
          vy: 0,
          radius: Math.min(8 + e.name.length * 1.5, 24),
          color: getColor(e.type),
        })
      }
    })

    const edges: GraphEdge[] = []
    relations.forEach((r) => {
      if (nodeMap.has(r.source_entity_id) && nodeMap.has(r.target_entity_id)) {
        edges.push({
          source: r.source_entity_id,
          target: r.target_entity_id,
          type: r.relation_type,
          weight: r.weight,
        })
      }
    })

    nodesRef.current = Array.from(nodeMap.values())
    edgesRef.current = edges
  }, [entities, relations, width, height])

  // Force simulation + rendering
  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return
    const ctx = canvas.getContext('2d')
    if (!ctx) return

    const dpr = window.devicePixelRatio || 1
    canvas.width = width * dpr
    canvas.height = height * dpr
    canvas.style.width = `${width}px`
    canvas.style.height = `${height}px`
    ctx.scale(dpr, dpr)

    let running = true

    const simulate = () => {
      if (!running) return
      const nodes = nodesRef.current
      const edges = edgesRef.current

      // Forces
      // Repulsion between nodes
      for (let i = 0; i < nodes.length; i++) {
        for (let j = i + 1; j < nodes.length; j++) {
          const dx = nodes[j].x - nodes[i].x
          const dy = nodes[j].y - nodes[i].y
          const dist = Math.sqrt(dx * dx + dy * dy) || 1
          const force = 800 / (dist * dist)
          const fx = (dx / dist) * force
          const fy = (dy / dist) * force
          nodes[i].vx -= fx
          nodes[i].vy -= fy
          nodes[j].vx += fx
          nodes[j].vy += fy
        }
      }

      // Attraction along edges
      for (const edge of edges) {
        const source = nodes.find((n) => n.id === edge.source)
        const target = nodes.find((n) => n.id === edge.target)
        if (!source || !target) continue

        const dx = target.x - source.x
        const dy = target.y - source.y
        const dist = Math.sqrt(dx * dx + dy * dy) || 1
        const force = (dist - 120) * 0.005
        const fx = (dx / dist) * force
        const fy = (dy / dist) * force
        source.vx += fx
        source.vy += fy
        target.vx -= fx
        target.vy -= fy
      }

      // Center gravity
      for (const node of nodes) {
        node.vx += (width / 2 - node.x) * 0.001
        node.vy += (height / 2 - node.y) * 0.001
      }

      // Apply velocity with damping
      for (const node of nodes) {
        if (dragRef.current.node === node) continue
        node.vx *= 0.85
        node.vy *= 0.85
        node.x += node.vx
        node.y += node.vy
        // Keep in bounds
        node.x = Math.max(node.radius, Math.min(width - node.radius, node.x))
        node.y = Math.max(node.radius, Math.min(height - node.radius, node.y))
      }

      // Draw
      ctx.clearRect(0, 0, width, height)
      ctx.fillStyle = '#fafafa'
      ctx.fillRect(0, 0, width, height)

      // Draw edges
      for (const edge of edges) {
        const source = nodes.find((n) => n.id === edge.source)
        const target = nodes.find((n) => n.id === edge.target)
        if (!source || !target) continue

        ctx.beginPath()
        ctx.moveTo(source.x, source.y)
        ctx.lineTo(target.x, target.y)
        ctx.strokeStyle = '#d9d9d9'
        ctx.lineWidth = Math.max(0.5, edge.weight * 2)
        ctx.stroke()

        // Edge label
        const mx = (source.x + target.x) / 2
        const my = (source.y + target.y) / 2
        ctx.font = '9px sans-serif'
        ctx.fillStyle = '#8c8c8c'
        ctx.textAlign = 'center'
        ctx.fillText(edge.type, mx, my - 4)

        // Arrow
        const angle = Math.atan2(target.y - source.y, target.x - source.x)
        const arrowX = target.x - Math.cos(angle) * target.radius
        const arrowY = target.y - Math.sin(angle) * target.radius
        ctx.beginPath()
        ctx.moveTo(arrowX, arrowY)
        ctx.lineTo(
          arrowX - 8 * Math.cos(angle - Math.PI / 6),
          arrowY - 8 * Math.sin(angle - Math.PI / 6)
        )
        ctx.lineTo(
          arrowX - 8 * Math.cos(angle + Math.PI / 6),
          arrowY - 8 * Math.sin(angle + Math.PI / 6)
        )
        ctx.closePath()
        ctx.fillStyle = '#bfbfbf'
        ctx.fill()
      }

      // Draw nodes
      for (const node of nodes) {
        // Shadow
        ctx.beginPath()
        ctx.arc(node.x, node.y, node.radius + 2, 0, Math.PI * 2)
        ctx.fillStyle = 'rgba(0,0,0,0.06)'
        ctx.fill()

        // Node circle
        ctx.beginPath()
        ctx.arc(node.x, node.y, node.radius, 0, Math.PI * 2)
        ctx.fillStyle = hoveredNode?.id === node.id ? '#fff' : node.color
        ctx.fill()
        ctx.strokeStyle = node.color
        ctx.lineWidth = hoveredNode?.id === node.id ? 3 : 1.5
        ctx.stroke()

        // Label
        ctx.font = 'bold 10px sans-serif'
        ctx.fillStyle = hoveredNode?.id === node.id ? node.color : '#fff'
        ctx.textAlign = 'center'
        ctx.textBaseline = 'middle'
        const label = node.name.length > 6 ? node.name.slice(0, 5) + '…' : node.name
        ctx.fillText(label, node.x, node.y)

        // Type label below
        ctx.font = '8px sans-serif'
        ctx.fillStyle = '#595959'
        ctx.fillText(node.type, node.x, node.y + node.radius + 10)
      }

      animRef.current = requestAnimationFrame(simulate)
    }

    simulate()

    return () => {
      running = false
      cancelAnimationFrame(animRef.current)
    }
  }, [entities, relations, width, height, hoveredNode])

  // Mouse interaction
  const findNodeAt = useCallback(
    (x: number, y: number): GraphNode | null => {
      for (const node of nodesRef.current) {
        const dx = x - node.x
        const dy = y - node.y
        if (dx * dx + dy * dy <= node.radius * node.radius) return node
      }
      return null
    },
    []
  )

  const handleMouseMove = useCallback(
    (e: React.MouseEvent<HTMLCanvasElement>) => {
      const rect = canvasRef.current?.getBoundingClientRect()
      if (!rect) return
      const x = e.clientX - rect.left
      const y = e.clientY - rect.top

      if (dragRef.current.node) {
        dragRef.current.node.x = x + dragRef.current.offsetX
        dragRef.current.node.y = y + dragRef.current.offsetY
        dragRef.current.node.vx = 0
        dragRef.current.node.vy = 0
        return
      }

      const node = findNodeAt(x, y)
      setHoveredNode(node)
      setTooltipPos({ x: e.clientX, y: e.clientY })
      if (canvasRef.current) {
        canvasRef.current.style.cursor = node ? 'grab' : 'default'
      }
    },
    [findNodeAt]
  )

  const handleMouseDown = useCallback(
    (e: React.MouseEvent<HTMLCanvasElement>) => {
      const rect = canvasRef.current?.getBoundingClientRect()
      if (!rect) return
      const x = e.clientX - rect.left
      const y = e.clientY - rect.top
      const node = findNodeAt(x, y)
      if (node) {
        dragRef.current = { node, offsetX: node.x - x, offsetY: node.y - y }
        if (canvasRef.current) canvasRef.current.style.cursor = 'grabbing'
      }
    },
    [findNodeAt]
  )

  const handleMouseUp = useCallback(() => {
    dragRef.current = { node: null, offsetX: 0, offsetY: 0 }
    if (canvasRef.current) canvasRef.current.style.cursor = 'default'
  }, [])

  if (!entities.length) {
    return (
      <Card title={title}>
        <Empty description="暂无图谱数据" />
      </Card>
    )
  }

  // Collect unique types for legend
  const typeSet = new Set(entities.map((e) => e.type))

  return (
    <Card
      title={title}
      extra={
        <Space size={4} wrap>
          {Array.from(typeSet).map((t) => (
            <Tag key={t} color={getColor(t)}>
              {t}
            </Tag>
          ))}
          <Text type="secondary" style={{ fontSize: 12 }}>
            {entities.length} 实体 · {relations.length} 关系
          </Text>
        </Space>
      }
    >
      <div style={{ position: 'relative' }}>
        <canvas
          ref={canvasRef}
          style={{ borderRadius: 8, border: '1px solid #f0f0f0' }}
          onMouseMove={handleMouseMove}
          onMouseDown={handleMouseDown}
          onMouseUp={handleMouseUp}
          onMouseLeave={handleMouseUp}
        />
        {hoveredNode && (
          <Tooltip open title={`${hoveredNode.name} (${hoveredNode.type})`}>
            <div
              style={{
                position: 'fixed',
                left: tooltipPos.x,
                top: tooltipPos.y,
                pointerEvents: 'none',
              }}
            />
          </Tooltip>
        )}
      </div>
      <div style={{ marginTop: 8 }}>
        <Text type="secondary" style={{ fontSize: 12 }}>
          提示：拖拽节点可调整位置
        </Text>
      </div>
    </Card>
  )
}

export default GraphVisualization
