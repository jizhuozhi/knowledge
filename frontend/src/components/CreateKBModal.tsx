import { Form, Input, Modal } from 'antd'

interface CreateKBModalProps {
  visible: boolean
  onCancel: () => void
  onSubmit: (data: { name: string; description: string }) => void
}

const CreateKBModal = ({ visible, onCancel, onSubmit }: CreateKBModalProps) => {
  const [form] = Form.useForm()

  const handleOk = async () => {
    try {
      const values = await form.validateFields()
      onSubmit(values)
      form.resetFields()
    } catch {
      // Validation failed
    }
  }

  return (
    <Modal
      title="创建知识库"
      open={visible}
      onOk={handleOk}
      onCancel={() => {
        form.resetFields()
        onCancel()
      }}
      destroyOnClose
    >
      <Form
        form={form}
        layout="vertical"
      >
        <Form.Item
          name="name"
          label="知识库名称"
          rules={[{ required: true, message: '请输入知识库名称' }]}
        >
          <Input placeholder="请输入知识库名称" />
        </Form.Item>

        <Form.Item
          name="description"
          label="描述"
        >
          <Input.TextArea rows={3} placeholder="请输入知识库描述" />
        </Form.Item>

      </Form>
    </Modal>
  )
}

export default CreateKBModal
