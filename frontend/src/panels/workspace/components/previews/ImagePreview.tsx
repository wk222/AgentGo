import { defineComponent, ref, onMounted, watch, type PropType } from 'vue'
import { wailsCall } from '../../../../wails'

export const ImagePreview = defineComponent({
  name: 'ImagePreview',
  props: {
    filePath: { type: String, required: true },
    fileName: { type: String, required: true },
  },
  setup(props) {
    const dataUrl = ref('')
    const loading = ref(true)
    const errorMsg = ref('')
    const zoom = ref(100)
    const imgWidth = ref(0)
    const imgHeight = ref(0)
    const fileSize = ref(0)

    const loadImage = async () => {
      loading.value = true
      errorMsg.value = ''
      try {
        const res = await wailsCall<{
          success?: boolean
          data_url?: string
          size?: number
          error?: string
        }>('WorkspaceReadBase64', props.filePath)

        if (res?.error || !res?.data_url) {
          errorMsg.value = res?.error || '无法读取图片数据'
          return
        }

        dataUrl.value = res.data_url
        fileSize.value = res.size || 0
      } catch (e: any) {
        errorMsg.value = e?.message || '加载图片失败'
      } finally {
        loading.value = false
      }
    }

    const onImageLoaded = (e: Event) => {
      const img = e.target as HTMLImageElement
      imgWidth.value = img.naturalWidth
      imgHeight.value = img.naturalHeight
    }

    const formatSize = (bytes: number) => {
      if (bytes < 1024) return `${bytes} B`
      if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
      return `${(bytes / (1024 * 1024)).toFixed(2)} MB`
    }

    watch(() => props.filePath, () => {
      loadImage()
    })

    onMounted(() => {
      loadImage()
    })

    return {
      dataUrl,
      loading,
      errorMsg,
      zoom,
      imgWidth,
      imgHeight,
      fileSize,
      onImageLoaded,
      formatSize,
    }
  },
  render() {
    return (
      <div
        style={{
          display: 'flex',
          flexDirection: 'column',
          width: '100%',
          height: '100%',
          backgroundColor: '#18181B',
          overflow: 'hidden',
          userSelect: 'none',
        }}
      >
        {/* Top Controls Bar */}
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            padding: '6px 14px',
            backgroundColor: '#1E1E22',
            borderBottom: '1px solid rgba(255, 255, 255, 0.08)',
            fontSize: '12px',
            color: '#A1A1AA',
          }}
        >
          <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
            <span style={{ fontWeight: 600, color: '#FFFFFF' }}>{this.fileName}</span>
            {this.imgWidth > 0 && (
              <span style={{ color: '#71717A', fontSize: '11px' }}>
                {this.imgWidth} × {this.imgHeight} px • {this.formatSize(this.fileSize)}
              </span>
            )}
          </div>

          <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
            <button
              onClick={() => { this.zoom = Math.max(25, this.zoom - 25) }}
              style={{
                padding: '2px 8px',
                backgroundColor: 'rgba(255,255,255,0.06)',
                color: '#E4E4E7',
                border: 'none',
                borderRadius: '3px',
                cursor: 'pointer',
              }}
            >
              −
            </button>
            <span style={{ minWidth: '42px', textAlign: 'center', fontSize: '11px' }}>{this.zoom}%</span>
            <button
              onClick={() => { this.zoom = Math.min(400, this.zoom + 25) }}
              style={{
                padding: '2px 8px',
                backgroundColor: 'rgba(255,255,255,0.06)',
                color: '#E4E4E7',
                border: 'none',
                borderRadius: '3px',
                cursor: 'pointer',
              }}
            >
              +
            </button>
            <button
              onClick={() => { this.zoom = 100 }}
              style={{
                padding: '2px 8px',
                backgroundColor: 'rgba(255,255,255,0.06)',
                color: '#A1A1AA',
                border: 'none',
                borderRadius: '3px',
                cursor: 'pointer',
                fontSize: '11px',
              }}
            >
              重置
            </button>
          </div>
        </div>

        {/* Image Preview Canvas Area */}
        <div
          style={{
            flex: 1,
            overflow: 'auto',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            padding: '24px',
            backgroundImage: `
              linear-gradient(45deg, #222226 25%, transparent 25%),
              linear-gradient(-45deg, #222226 25%, transparent 25%),
              linear-gradient(45deg, transparent 75%, #222226 75%),
              linear-gradient(-45deg, transparent 75%, #222226 75%)
            `,
            backgroundSize: '20px 20px',
            backgroundPosition: '0 0, 0 10px, 10px -10px, -10px 0px',
            backgroundColor: '#18181B',
          }}
        >
          {this.loading ? (
            <div style={{ color: '#71717A', fontSize: '13px' }}>加载图片中...</div>
          ) : this.errorMsg ? (
            <div style={{ color: '#EF4444', fontSize: '13px' }}>{this.errorMsg}</div>
          ) : (
            <img
              src={this.dataUrl}
              alt={this.fileName}
              onLoad={this.onImageLoaded}
              style={{
                width: `${this.zoom}%`,
                maxWidth: 'none',
                objectFit: 'contain',
                boxShadow: '0 8px 24px rgba(0,0,0,0.5)',
                transition: 'width 0.15s ease',
              }}
            />
          )}
        </div>
      </div>
    )
  },
})
