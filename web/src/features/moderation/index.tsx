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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { toIntlLocale } from '@/i18n/languages'

import {
  getModerationLogs,
  getModerationSettings,
  updateModerationSettings,
  type ModerationMode,
} from './api'

const settingsKey = ['moderation-settings']
const logsKey = ['moderation-logs']

export function ModerationPage() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const queryClient = useQueryClient()
  const settings = useQuery({
    queryKey: settingsKey,
    queryFn: getModerationSettings,
  })
  const logs = useQuery({ queryKey: logsKey, queryFn: getModerationLogs })
  const [mode, setMode] = useState<ModerationMode>('auto')
  const [blocklist, setBlocklist] = useState('')

  useEffect(() => {
    if (!settings.data) return
    setMode(settings.data.enabled)
    setBlocklist(settings.data.blocklist)
  }, [settings.data])

  const save = useMutation({
    mutationFn: () => updateModerationSettings({ enabled: mode, blocklist }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: settingsKey })
      toast.success(t('Content moderation settings saved'))
    },
  })

  const credentialText = settings.data?.credentials_present
    ? t('Waffo merchant credentials are present')
    : t('Waffo merchant credentials are not present')
  const effectiveText = settings.data?.effective_enabled
    ? t('Prompt Sift is on for generation requests')
    : t('Prompt Sift is off')
  const modeItems = [
    {
      value: 'auto',
      label: t('Auto (on when Waffo merchant credentials exist)'),
    },
    { value: 'true', label: t('On') },
    { value: 'false', label: t('Off') },
  ]

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Content moderation')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='max-w-4xl space-y-6'>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Prompt Sift checks user text before a generation request is sent upstream. Text outputs are checked against the blocklist. Image and video pixels are not scanned. A review verdict is rejected immediately.'
            )}
          </p>
          <form
            className='space-y-4'
            onSubmit={(event) => {
              event.preventDefault()
              save.mutate()
            }}
          >
            <div className='space-y-2'>
              <Label htmlFor='moderation-mode'>{t('Mode')}</Label>
              <Select
                items={modeItems}
                value={mode}
                onValueChange={(value) => {
                  if (
                    value === 'auto' ||
                    value === 'true' ||
                    value === 'false'
                  ) {
                    setMode(value)
                  }
                }}
              >
                <SelectTrigger id='moderation-mode' className='w-full max-w-md'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value='auto'>
                      {t('Auto (on when Waffo merchant credentials exist)')}
                    </SelectItem>
                    <SelectItem value='true'>{t('On')}</SelectItem>
                    <SelectItem value='false'>{t('Off')}</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
            </div>
            <p className='text-sm'>{credentialText}</p>
            <p className='text-sm'>{effectiveText}</p>
            <div className='space-y-2'>
              <Label htmlFor='moderation-blocklist'>
                {t('Output blocklist')}
              </Label>
              <Textarea
                id='moderation-blocklist'
                value={blocklist}
                onChange={(event) => setBlocklist(event.target.value)}
                rows={12}
                spellCheck={false}
              />
              <p className='text-muted-foreground text-sm'>
                {t(
                  'One term per line, as category|term. Saving an empty list keeps the built-in seed. The check matches text only.'
                )}
              </p>
            </div>
            <Button
              type='submit'
              disabled={save.isPending || settings.isLoading}
            >
              {t('Save')}
            </Button>
          </form>
          <div className='space-y-2'>
            <h2 className='text-base font-medium'>{t('Moderation logs')}</h2>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('Time')}</TableHead>
                  <TableHead>{t('Stage')}</TableHead>
                  <TableHead>{t('Verdict')}</TableHead>
                  <TableHead>{t('Model')}</TableHead>
                  <TableHead>{t('Categories')}</TableHead>
                  <TableHead>{t('Excerpt')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(logs.data?.items ?? []).map((item) => (
                  <TableRow key={item.id}>
                    <TableCell>
                      {new Date(item.created_at * 1000).toLocaleString(locale)}
                    </TableCell>
                    <TableCell>{item.stage}</TableCell>
                    <TableCell>{item.verdict}</TableCell>
                    <TableCell>{item.model_name}</TableCell>
                    <TableCell>{item.categories}</TableCell>
                    <TableCell>{item.excerpt}</TableCell>
                  </TableRow>
                ))}
                {logs.data && logs.data.items.length === 0 ? (
                  <TableRow>
                    <TableCell colSpan={6}>{t('No moderation logs')}</TableCell>
                  </TableRow>
                ) : null}
              </TableBody>
            </Table>
          </div>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
