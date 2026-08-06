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
import { ArrowRight, ChevronLeft, ChevronRight } from 'lucide-react'
import { useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

const PAGE_SIZES = [10, 20, 50, 100, 200, 500]

type LiandongTablePaginationProps = {
  countLabel: ReactNode
  page: number
  pageSize: number
  total: number
  onPageChange: (page: number) => void
  onPageSizeChange: (pageSize: number) => void
}

export function LiandongTablePagination(props: LiandongTablePaginationProps) {
  const { t } = useTranslation()
  const [pageInput, setPageInput] = useState(String(props.page))
  const totalPages = Math.max(1, Math.ceil(props.total / props.pageSize))

  useEffect(() => {
    setPageInput(String(props.page))
  }, [props.page])

  const jumpToPage = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const parsedPage = Number.parseInt(pageInput, 10)
    const targetPage = Number.isFinite(parsedPage)
      ? Math.min(totalPages, Math.max(1, parsedPage))
      : props.page
    setPageInput(String(targetPage))
    props.onPageChange(targetPage)
  }

  return (
    <div className='flex flex-wrap items-center justify-between gap-3'>
      <p className='text-muted-foreground text-xs'>{props.countLabel}</p>
      <div className='flex flex-wrap items-center justify-end gap-2'>
        <span className='text-muted-foreground text-xs whitespace-nowrap'>
          {t('Rows per page')}
        </span>
        <Select
          items={PAGE_SIZES.map((size) => ({
            value: String(size),
            label: String(size),
          }))}
          value={String(props.pageSize)}
          onValueChange={(value) => {
            const nextPageSize = Number(value)
            if (!PAGE_SIZES.includes(nextPageSize)) return
            props.onPageSizeChange(nextPageSize)
          }}
        >
          <SelectTrigger className='h-8 w-20' aria-label={t('Rows per page')}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent alignItemWithTrigger={false}>
            <SelectGroup>
              {PAGE_SIZES.map((size) => (
                <SelectItem key={size} value={String(size)}>
                  {size}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
        <Button
          type='button'
          variant='outline'
          size='icon-sm'
          onClick={() => props.onPageChange(Math.max(1, props.page - 1))}
          disabled={props.page <= 1}
          aria-label={t('Previous page')}
        >
          <ChevronLeft className='size-4' />
        </Button>
        <span className='min-w-14 text-center text-sm tabular-nums'>
          {props.page} / {totalPages}
        </span>
        <Button
          type='button'
          variant='outline'
          size='icon-sm'
          onClick={() =>
            props.onPageChange(Math.min(totalPages, props.page + 1))
          }
          disabled={props.page >= totalPages}
          aria-label={t('Next page')}
        >
          <ChevronRight className='size-4' />
        </Button>
        <form className='flex items-center gap-1' onSubmit={jumpToPage}>
          <Input
            type='number'
            inputMode='numeric'
            min={1}
            max={totalPages}
            value={pageInput}
            onChange={(event) => setPageInput(event.target.value)}
            className='h-8 w-16 text-center tabular-nums'
            aria-label={t('Page')}
          />
          <Button
            type='submit'
            variant='outline'
            size='icon-sm'
            aria-label={t('Go to page {{page}}', {
              page: pageInput || props.page,
            })}
          >
            <ArrowRight className='size-4' />
          </Button>
        </form>
      </div>
    </div>
  )
}
