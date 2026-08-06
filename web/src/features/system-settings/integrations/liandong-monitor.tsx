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
import { useQuery } from '@tanstack/react-query'
import { Activity, RefreshCw } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import type { SystemTask } from '@/features/system-settings/types'

import {
  getLiandongMonitorTasks,
  listLiandongMonitorCalls,
} from './liandong-api'
import { LiandongTablePagination } from './liandong-table-pagination'

const TASK_STATUSES = ['all', 'pending', 'running', 'succeeded', 'failed']
const CALL_RESULTS = ['all', 'success', 'failed']

function formatTimestamp(timestamp: number): string {
  return timestamp > 0 ? new Date(timestamp * 1000).toLocaleString() : '-'
}

function callSourceLabel(source: string, t: (key: string) => string): string {
  const labels: Record<string, string> = {
    scheduled_reconcile: 'Scheduled reconciliation',
    client_order_poll: 'Client order polling',
    user_order_create: 'User order creation',
    user_order_close: 'User order closing',
    root_order_close: 'Root order closing',
    provider_goods: 'Provider goods query',
    payment_probe: 'Payment page monitoring',
    user_payment_page: 'User payment page',
    proxy_validation: 'Proxy connectivity check',
    unspecified: 'Other card marketplace call',
  }
  return t(labels[source] || source)
}

function operationLabel(operation: string, t: (key: string) => string): string {
  const labels: Record<string, string> = {
    create_order: 'Create payment order',
    payment_page_probe: 'Probe payment page',
    query_orders: 'Query payment status',
    query_goods: 'Query provider goods',
    login: 'Refresh provider login',
    proxy_validation: 'Validate proxy connection',
  }
  return t(labels[operation] || operation)
}

function callResultLabel(result: string, t: (key: string) => string): string {
  if (result === 'all') return t('All')
  if (result === 'success') return t('Success')
  return t('Failed')
}

function taskStatusVariant(status: string): StatusVariant {
  if (status === 'succeeded') return 'success'
  if (status === 'failed') return 'danger'
  if (status === 'running') return 'info'
  return 'warning'
}

function taskResult(task: SystemTask, t: (key: string) => string): string {
  if (task.error) return task.error
  if (!task.result || typeof task.result !== 'object') return '-'
  const result = task.result as Record<string, unknown>
  const fields = [
    ['processed', t('Processed')],
    ['paid', t('Paid')],
    ['fulfilled', t('Fulfilled')],
    ['failed', t('Failed')],
  ]
  const values = fields
    .filter(([key]) => typeof result[key] === 'number')
    .map(([key, label]) => `${label}: ${String(result[key])}`)
  return values.length > 0 ? values.join(' · ') : '-'
}

function callPayload(payload?: string): ReactNode {
  if (!payload) return <span className='text-muted-foreground'>-</span>
  return (
    <pre className='bg-muted/40 max-h-24 max-w-80 overflow-auto p-2 font-mono text-[11px] leading-4 break-all whitespace-pre-wrap'>
      {payload}
    </pre>
  )
}

export function LiandongMonitor() {
  const { t } = useTranslation()
  const [taskPage, setTaskPage] = useState(1)
  const [taskPageSize, setTaskPageSize] = useState(10)
  const [taskStatus, setTaskStatus] = useState('running')
  const [callPage, setCallPage] = useState(1)
  const [callPageSize, setCallPageSize] = useState(10)
  const [callResult, setCallResult] = useState('success')
  const tasksQuery = useQuery({
    queryKey: ['liandong-monitor', 'tasks', taskPage, taskPageSize, taskStatus],
    queryFn: async () => {
      const response = await getLiandongMonitorTasks(
        taskPage,
        taskPageSize,
        taskStatus
      )
      if (!response.success || !response.data) {
        throw new Error(response.message || t('Failed to load monitoring data'))
      }
      return response.data
    },
    refetchOnReconnect: false,
    refetchOnWindowFocus: false,
    retry: false,
    staleTime: Infinity,
  })
  const callsQuery = useQuery({
    queryKey: ['liandong-monitor', 'calls', callPage, callPageSize, callResult],
    queryFn: async () => {
      const response = await listLiandongMonitorCalls(
        callPage,
        callPageSize,
        callResult
      )
      if (!response.success || !response.data) {
        throw new Error(response.message || t('Failed to load monitoring data'))
      }
      return response.data
    },
    refetchOnReconnect: false,
    refetchOnWindowFocus: false,
    retry: false,
    staleTime: Infinity,
  })

  const tasks = tasksQuery.data?.items || []
  const totalTasks = tasksQuery.data?.total || 0
  const calls = callsQuery.data?.items || []
  const totalCalls = callsQuery.data?.total || 0
  const refreshing = tasksQuery.isFetching || callsQuery.isFetching
  const error = tasksQuery.error || callsQuery.error
  let schedulerLabel = t('Disabled')
  let schedulerVariant: StatusVariant = 'neutral'
  if (tasksQuery.data?.scheduler_active) {
    schedulerLabel = t('Running when due')
    schedulerVariant = 'success'
  } else if (tasksQuery.data?.scheduler_configured) {
    schedulerLabel = t('Waiting for work')
    schedulerVariant = 'info'
  }

  return (
    <div className='space-y-5 border-t pt-6'>
      <div className='flex flex-wrap items-start justify-between gap-3'>
        <div className='flex min-w-0 items-start gap-2.5'>
          <span className='bg-muted text-muted-foreground inline-flex size-8 shrink-0 items-center justify-center rounded-md'>
            <Activity className='size-4' aria-hidden='true' />
          </span>
          <div>
            <h4 className='font-medium'>{t('Card marketplace monitoring')}</h4>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Shows task-level scheduler history together with recent failed card marketplace calls and login refresh records. Request and response payloads are sanitized and truncated; credentials, headers, and cookies are never recorded.'
              )}
            </p>
          </div>
        </div>
        <Button
          type='button'
          variant='outline'
          size='icon-sm'
          onClick={() =>
            void Promise.all([tasksQuery.refetch(), callsQuery.refetch()])
          }
          disabled={refreshing}
          aria-label={t('Refresh')}
        >
          <RefreshCw
            className={refreshing ? 'size-4 animate-spin' : 'size-4'}
          />
        </Button>
      </div>

      {error && (
        <p className='text-destructive text-sm'>
          {error instanceof Error
            ? error.message
            : t('Failed to load monitoring data')}
        </p>
      )}

      <div className='grid gap-3 border-y py-4 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center'>
        <div className='min-w-0'>
          <div className='flex flex-wrap items-center gap-2'>
            <p className='text-sm font-medium'>
              {t('Global payment maintenance task')}
            </p>
            <StatusBadge
              label={schedulerLabel}
              variant={schedulerVariant}
              copyable={false}
            />
          </div>
          <p className='text-muted-foreground mt-1 text-xs'>
            {t(
              'This task-level history summarizes each scheduler run and its aggregate result. It is separate from the request-level diagnostics below.'
            )}
          </p>
        </div>
        <div className='text-sm tabular-nums'>
          {t('Interval')}: {tasksQuery.data?.poll_interval_seconds ?? '-'}{' '}
          {t('seconds')}
        </div>
      </div>

      <div className='space-y-2'>
        <div className='flex flex-wrap items-end justify-between gap-3'>
          <div>
            <h5 className='text-sm font-medium'>
              {t('Scheduled task history')}
            </h5>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Recent executions recorded by the shared system task scheduler.'
              )}
            </p>
          </div>
          <div className='grid gap-1'>
            <span className='text-muted-foreground text-xs'>{t('Status')}</span>
            <Select
              items={TASK_STATUSES.map((status) => ({
                value: status,
                label: status === 'all' ? t('All') : t(status),
              }))}
              value={taskStatus}
              onValueChange={(status) => {
                if (!status) return
                setTaskStatus(status)
                setTaskPage(1)
              }}
            >
              <SelectTrigger className='h-8 w-40' aria-label={t('Status')}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent alignItemWithTrigger={false}>
                <SelectGroup>
                  {TASK_STATUSES.map((status) => (
                    <SelectItem key={status} value={status}>
                      {status === 'all' ? t('All') : t(status)}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </div>
        </div>
        <StaticDataTable
          tableClassName='min-w-[860px] table-fixed'
          data={tasks}
          getRowKey={(task) => task.task_id}
          emptyContent={t('No scheduled task records')}
          columns={[
            {
              id: 'task',
              header: t('Task'),
              className: 'w-[38%]',
              cellClassName: 'w-[38%]',
              cell: (task) => (
                <div>
                  <p className='text-sm'>{t('Global payment maintenance')}</p>
                  <p className='text-muted-foreground font-mono text-[11px]'>
                    {task.task_id}
                  </p>
                </div>
              ),
            },
            {
              id: 'status',
              header: t('Status'),
              className: 'w-32',
              cellClassName: 'w-32',
              cell: (task) => (
                <StatusBadge
                  label={t(task.status)}
                  variant={taskStatusVariant(task.status)}
                  copyable={false}
                />
              ),
            },
            {
              id: 'result',
              header: t('Result'),
              className: 'w-[34%]',
              cellClassName: 'w-[34%]',
              cell: (task) => (
                <p
                  className={
                    task.error
                      ? 'text-destructive max-w-[420px] text-xs break-words'
                      : 'text-muted-foreground text-xs'
                  }
                >
                  {taskResult(task, t)}
                </p>
              ),
            },
            {
              id: 'updated',
              header: t('Updated'),
              className: 'w-48',
              cellClassName: 'w-48',
              cell: (task) => (
                <span className='text-xs whitespace-nowrap'>
                  {formatTimestamp(task.updated_at)}
                </span>
              ),
            },
          ]}
        />
        <LiandongTablePagination
          countLabel={t('{{count}} task records', { count: totalTasks })}
          page={taskPage}
          pageSize={taskPageSize}
          total={totalTasks}
          onPageChange={setTaskPage}
          onPageSizeChange={(nextPageSize) => {
            setTaskPageSize(nextPageSize)
            setTaskPage(1)
          }}
        />
      </div>

      <div className='space-y-2'>
        <div className='flex flex-wrap items-end justify-between gap-3'>
          <div>
            <h5 className='text-sm font-medium'>
              {t('Upstream call records')}
            </h5>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Only failed logical requests and login refresh attempts are retained. Browser verification retries that recover successfully are not reported as failures.'
              )}
            </p>
          </div>
          <div className='grid gap-1'>
            <span className='text-muted-foreground text-xs'>{t('Result')}</span>
            <Select
              items={CALL_RESULTS.map((result) => ({
                value: result,
                label: callResultLabel(result, t),
              }))}
              value={callResult}
              onValueChange={(result) => {
                if (!result) return
                setCallResult(result)
                setCallPage(1)
              }}
            >
              <SelectTrigger className='h-8 w-40' aria-label={t('Result')}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent alignItemWithTrigger={false}>
                <SelectGroup>
                  <SelectItem value='all'>{t('All')}</SelectItem>
                  <SelectItem value='success'>{t('Success')}</SelectItem>
                  <SelectItem value='failed'>{t('Failed')}</SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
          </div>
        </div>
        <StaticDataTable
          tableClassName='min-w-[1780px]'
          data={calls}
          getRowKey={(call) => call.request_id}
          emptyContent={t('No upstream call records')}
          columns={[
            {
              id: 'time',
              header: t('Time'),
              cell: (call) => (
                <span className='text-xs whitespace-nowrap'>
                  {formatTimestamp(call.created_at)}
                </span>
              ),
            },
            {
              id: 'source',
              header: t('Source'),
              cell: (call) => (
                <div>
                  <p className='text-sm'>{callSourceLabel(call.source, t)}</p>
                  {call.reference && (
                    <p className='text-muted-foreground max-w-48 truncate font-mono text-[11px]'>
                      {call.reference}
                    </p>
                  )}
                </div>
              ),
            },
            {
              id: 'operation',
              header: t('Operation'),
              cell: (call) => (
                <div>
                  <p className='text-sm'>{operationLabel(call.operation, t)}</p>
                  <p className='text-muted-foreground font-mono text-[11px]'>
                    {call.method} {call.path}
                  </p>
                </div>
              ),
            },
            {
              id: 'request_body',
              header: t('Request'),
              cell: (call) => callPayload(call.request_body),
            },
            {
              id: 'response_body',
              header: t('Response'),
              cell: (call) => callPayload(call.response_body),
            },
            {
              id: 'result',
              header: t('Result'),
              cell: (call) => (
                <div className='flex items-center gap-2'>
                  <StatusBadge
                    label={call.success ? t('Success') : t('Failed')}
                    variant={call.success ? 'success' : 'danger'}
                    copyable={false}
                  />
                  <span className='text-muted-foreground text-xs tabular-nums'>
                    {call.status_code > 0 ? `HTTP ${call.status_code}` : '-'}
                  </span>
                </div>
              ),
            },
            {
              id: 'duration',
              header: t('Duration'),
              cell: (call) => (
                <span className='text-xs tabular-nums'>
                  {call.duration_ms} ms
                </span>
              ),
            },
            {
              id: 'error',
              header: t('Error detail'),
              cell: (call) => (
                <p
                  className='text-destructive max-w-[360px] text-xs break-words'
                  title={call.error || undefined}
                >
                  {call.error || '-'}
                </p>
              ),
            },
          ]}
        />
        <LiandongTablePagination
          countLabel={t('{{count}} call records', { count: totalCalls })}
          page={callPage}
          pageSize={callPageSize}
          total={totalCalls}
          onPageChange={setCallPage}
          onPageSizeChange={(nextPageSize) => {
            setCallPageSize(nextPageSize)
            setCallPage(1)
          }}
        />
      </div>
    </div>
  )
}
