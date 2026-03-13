import { useMemo } from 'react'
import { Typography } from 'antd'

const { Text } = Typography

interface WordCloudProps {
  words: string[]
  maxWords?: number
  height?: number
}

// Generate a simple frequency map from words
function computeWordFrequency(words: string[]): Map<string, number> {
  const freq = new Map<string, number>()
  for (const word of words) {
    const normalized = word.toLowerCase().trim()
    if (normalized) {
      freq.set(normalized, (freq.get(normalized) || 0) + 1)
    }
  }
  return freq
}

// Color palette for word cloud
const colorPalette = [
  '#1890ff', '#52c41a', '#722ed1', '#13c2c2', '#eb2f96',
  '#fa8c16', '#2f54eb', '#faad14', '#a0d911', '#f5222d',
]

const WordCloud: React.FC<WordCloudProps> = ({ words, maxWords = 20, height = 120 }) => {
  const cloudData = useMemo(() => {
    const freq = computeWordFrequency(words)
    
    // Sort by frequency and take top N
    const sorted = Array.from(freq.entries())
      .sort((a, b) => b[1] - a[1])
      .slice(0, maxWords)
    
    if (sorted.length === 0) return []
    
    const maxFreq = sorted[0][1]
    const minFreq = sorted[sorted.length - 1][1]
    const range = maxFreq - minFreq || 1
    
    return sorted.map(([word, count], index) => {
      // Normalize frequency to 0-1 range
      const normalized = (count - minFreq) / range
      
      // Scale font size: 12px to 28px
      const fontSize = 12 + Math.round(normalized * 16)
      
      // Assign color from palette
      const color = colorPalette[index % colorPalette.length]
      
      return {
        word,
        count,
        fontSize,
        color,
        // Random slight rotation for visual interest
        rotation: Math.random() > 0.7 ? (Math.random() > 0.5 ? -5 : 5) : 0,
      }
    })
  }, [words, maxWords])

  if (cloudData.length === 0) {
    return <Text type="secondary">暂无关键词</Text>
  }

  return (
    <div
      style={{
        height,
        display: 'flex',
        flexWrap: 'wrap',
        alignItems: 'center',
        justifyContent: 'center',
        gap: '8px 12px',
        padding: '8px',
        overflow: 'hidden',
      }}
    >
      {cloudData.map((item, idx) => (
        <span
          key={`${item.word}-${idx}`}
          style={{
            fontSize: item.fontSize,
            color: item.color,
            fontWeight: item.fontSize > 20 ? 600 : 400,
            transform: `rotate(${item.rotation}deg)`,
            display: 'inline-block',
            transition: 'all 0.2s ease',
            cursor: 'default',
          }}
          title={`${item.word}: ${item.count}次`}
        >
          {item.word}
        </span>
      ))}
    </div>
  )
}

export default WordCloud
