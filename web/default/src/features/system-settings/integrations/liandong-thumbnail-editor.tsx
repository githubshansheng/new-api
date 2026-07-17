/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { ImagePlus, Loader2, Trash2, ZoomIn } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Slider } from '@/components/ui/slider'

type Props = {
  currentUrl?: string
  deleting?: boolean
  onBlobChange: (blob: Blob | null) => void
  onDelete?: () => void
}

export function LiandongThumbnailEditor(props: Props) {
  const { t } = useTranslation()
  const onBlobChange = props.onBlobChange
  const inputRef = useRef<HTMLInputElement>(null)
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const imageRef = useRef<HTMLImageElement | null>(null)
  const objectUrlRef = useRef('')
  const [previewUrl, setPreviewUrl] = useState('')
  const [zoom, setZoom] = useState(1)
  const [processing, setProcessing] = useState(false)

  const renderCrop = useCallback(async () => {
    const image = imageRef.current
    const canvas = canvasRef.current
    if (!image || !canvas) return

    setProcessing(true)
    const context = canvas.getContext('2d')
    if (!context) {
      setProcessing(false)
      return
    }
    const sourceSize = Math.min(image.naturalWidth, image.naturalHeight) / zoom
    const sourceX = (image.naturalWidth - sourceSize) / 2
    const sourceY = (image.naturalHeight - sourceSize) / 2
    context.clearRect(0, 0, canvas.width, canvas.height)
    context.imageSmoothingEnabled = true
    context.imageSmoothingQuality = 'high'
    context.drawImage(
      image,
      sourceX,
      sourceY,
      sourceSize,
      sourceSize,
      0,
      0,
      canvas.width,
      canvas.height
    )
    const blob = await new Promise<Blob | null>((resolve) => {
      canvas.toBlob(resolve, 'image/webp', 0.86)
    })
    onBlobChange(blob)
    setProcessing(false)
  }, [onBlobChange, zoom])

  useEffect(() => {
    if (!imageRef.current) return
    void renderCrop()
  }, [renderCrop, zoom])

  useEffect(
    () => () => {
      if (objectUrlRef.current) URL.revokeObjectURL(objectUrlRef.current)
    },
    []
  )

  const selectFile = (file?: File) => {
    if (!file) return
    if (objectUrlRef.current) URL.revokeObjectURL(objectUrlRef.current)
    const objectUrl = URL.createObjectURL(file)
    objectUrlRef.current = objectUrl
    setPreviewUrl(objectUrl)
    setZoom(1)

    const image = new Image()
    image.addEventListener(
      'load',
      () => {
        imageRef.current = image
        void renderCrop()
      },
      { once: true }
    )
    image.src = objectUrl
  }

  const displayedUrl = previewUrl || props.currentUrl

  return (
    <div className='space-y-3'>
      <div className='flex flex-wrap items-start gap-4'>
        <div className='bg-muted relative h-40 w-40 shrink-0 overflow-hidden rounded-md border'>
          {displayedUrl ? (
            <img
              src={displayedUrl}
              alt={t('Product thumbnail preview')}
              className='h-full w-full object-cover'
            />
          ) : (
            <div className='text-muted-foreground flex h-full flex-col items-center justify-center gap-2 text-xs'>
              <ImagePlus className='h-8 w-8' />
              {t('No thumbnail')}
            </div>
          )}
          {processing && (
            <div className='bg-background/70 absolute inset-0 flex items-center justify-center'>
              <Loader2 className='h-5 w-5 animate-spin' />
            </div>
          )}
        </div>

        <div className='min-w-52 flex-1 space-y-3'>
          <input
            ref={inputRef}
            type='file'
            accept='image/jpeg,image/png,image/webp'
            className='sr-only'
            onChange={(event) => {
              selectFile(event.target.files?.[0])
              event.target.value = ''
            }}
          />
          <div className='flex flex-wrap gap-2'>
            <Button
              type='button'
              variant='outline'
              onClick={() => inputRef.current?.click()}
            >
              <ImagePlus className='h-4 w-4' />
              {displayedUrl ? t('Replace thumbnail') : t('Upload thumbnail')}
            </Button>
            {props.currentUrl && props.onDelete && (
              <Button
                type='button'
                variant='outline'
                className='text-destructive'
                disabled={props.deleting}
                onClick={props.onDelete}
              >
                {props.deleting ? (
                  <Loader2 className='h-4 w-4 animate-spin' />
                ) : (
                  <Trash2 className='h-4 w-4' />
                )}
                {t('Delete thumbnail')}
              </Button>
            )}
          </div>

          {previewUrl && (
            <div className='space-y-2'>
              <Label className='flex items-center gap-2 text-xs'>
                <ZoomIn className='h-4 w-4' />
                {t('Crop zoom')}
              </Label>
              <Slider
                min={1}
                max={3}
                step={0.05}
                value={[zoom]}
                onValueChange={(value) =>
                  setZoom(
                    Array.isArray(value) ? Number(value[0] || 1) : Number(value)
                  )
                }
                aria-label={t('Crop zoom')}
              />
            </div>
          )}
          <p className='text-muted-foreground text-xs'>
            {t(
              'The image is center-cropped to a square and uploaded at 440 by 440 pixels. JPEG, PNG, or WebP; maximum 512 KB after cropping.'
            )}
          </p>
        </div>
      </div>
      <canvas ref={canvasRef} width={440} height={440} className='hidden' />
    </div>
  )
}
