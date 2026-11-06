import * as React from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { handleServerError } from '@/lib/handle-server-error'

import { getSystemOptions } from '../api'
import { useUpdateOption } from '../hooks/use-update-option'

type OptionRow = { key: string; value: string }

// EpayV2Settings manages the RSA based Epay V2 configuration. The merchant
// private key and the platform public key are write-only: the backend hides any
// option whose name ends in "Key", so they are shown as configured or not
// instead of being echoed back into the browser.
export function EpayV2Settings() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const updateOption = useUpdateOption()

  const [enabled, setEnabled] = React.useState(false)
  const [configured, setConfigured] = React.useState(false)
  const [loaded, setLoaded] = React.useState(false)
  const [privateKey, setPrivateKey] = React.useState('')
  const [publicKey, setPublicKey] = React.useState('')
  const [saving, setSaving] = React.useState(false)

  React.useEffect(() => {
    let cancelled = false
    getSystemOptions()
      .then((res) => {
        if (cancelled) return
        const rows = (res as { data?: OptionRow[] }).data ?? []
        const values = new Map(rows.map((row) => [row.key, row.value]))
        setEnabled(values.get('EpayV2Enabled') === 'true')
        setConfigured(values.get('EpayV2Configured') === 'true')
      })
      .catch(() => {
        /* keep defaults, the form still works */
      })
      .finally(() => {
        if (!cancelled) setLoaded(true)
      })
    return () => {
      cancelled = true
    }
  }, [])

  const onSave = async () => {
    const nextPrivateKey = privateKey.trim()
    const nextPublicKey = publicKey.trim()
    if (enabled && !configured && (!nextPrivateKey || !nextPublicKey)) {
      toast.error(
        t('Fill in both the merchant private key and the platform public key before enabling V2')
      )
      return
    }

    setSaving(true)
    try {
      await updateOption.mutateAsync({ key: 'EpayV2Enabled', value: enabled })
      if (nextPrivateKey) {
        await updateOption.mutateAsync({
          key: 'EpayMerchantPrivateKey',
          value: nextPrivateKey,
        })
      }
      if (nextPublicKey) {
        await updateOption.mutateAsync({
          key: 'EpayPlatformPublicKey',
          value: nextPublicKey,
        })
      }
      if (nextPrivateKey || nextPublicKey) setConfigured(true)
      setPrivateKey('')
      setPublicKey('')
      queryClient.invalidateQueries({ queryKey: ['system-options'] })
    } catch (error) {
      handleServerError(
        error as Error,
        t('Failed to save Epay V2 settings')
      )
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className='space-y-3 rounded-md border p-4'>
      <div>
        <h4 className='text-sm font-medium'>{t('Epay V2 (RSA)')}</h4>
        <p className='text-muted-foreground mt-1 text-xs leading-5'>
          {t(
            'V2 signs requests with your merchant RSA private key (SHA256WithRSA) and verifies callbacks with the platform public key. When disabled, the legacy MD5 flow keeps working.'
          )}
        </p>
      </div>

      <div className='flex items-center justify-between gap-3'>
        <div className='space-y-1'>
          <p className='text-sm'>{t('Enable Epay V2')}</p>
          <p className='text-muted-foreground text-xs'>
            {loaded
              ? configured
                ? t('Keys configured')
                : t('Keys not configured')
              : t('Loading...')}
          </p>
        </div>
        <Switch checked={enabled} onCheckedChange={setEnabled} />
      </div>

      <div className='space-y-1'>
        <p className='text-sm'>{t('Merchant private key (PEM)')}</p>
        <Textarea
          value={privateKey}
          onChange={(event) => setPrivateKey(event.target.value)}
          placeholder='-----BEGIN PRIVATE KEY-----'
          rows={5}
          className='font-mono text-xs'
          autoComplete='off'
          spellCheck={false}
        />
        <p className='text-muted-foreground text-xs'>
          {t('Leave blank to keep the stored private key')}
        </p>
      </div>

      <div className='space-y-1'>
        <p className='text-sm'>{t('Platform public key (PEM)')}</p>
        <Textarea
          value={publicKey}
          onChange={(event) => setPublicKey(event.target.value)}
          placeholder='-----BEGIN PUBLIC KEY-----'
          rows={5}
          className='font-mono text-xs'
          autoComplete='off'
          spellCheck={false}
        />
        <p className='text-muted-foreground text-xs'>
          {t('Leave blank to keep the stored public key')}
        </p>
      </div>

      <Button type='button' size='sm' onClick={onSave} disabled={saving}>
        {saving ? t('Saving...') : t('Save Epay V2 settings')}
      </Button>
    </div>
  )
}
