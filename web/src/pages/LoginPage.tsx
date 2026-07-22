import { Alert, Button, Collapse, Form, Input, Typography } from 'antd'
import { useEffect, useRef, useState } from 'react'
import { Navigate, useLocation, useNavigate } from 'react-router-dom'

import { api, safeReturnTo } from '../api/client'
import type { CaptchaChallenge, SMSChallenge } from '../api/types'
import { authStore, useAuthState } from '../auth/store'
import { RequestError } from '../components/RequestError'

const { Paragraph, Text, Title } = Typography
const mainlandPhonePattern = /^1[3-9]\d{9}$/
const smsCodePattern = /^\d{6}$/
const captchaWidth = 300
const captchaHeight = 220
const captchaTileSize = 64

interface SMSLoginValues {
  phone: string
  code: string
}

interface LocalLoginValues {
  username: string
  password: string
}

export function LoginPage() {
  const auth = useAuthState()
  const location = useLocation()
  const navigate = useNavigate()
  const [smsForm] = Form.useForm<SMSLoginValues>()
  const [captcha, setCaptcha] = useState<CaptchaChallenge>()
  const [captchaLoading, setCaptchaLoading] = useState(true)
  const [sliderX, setSliderX] = useState(0)
  const [smsChallenge, setSMSChallenge] = useState<SMSChallenge>()
  const [retryAfter, setRetryAfter] = useState(0)
  const [smsError, setSMSError] = useState<unknown>()
  const [localError, setLocalError] = useState<unknown>()
  const [sending, setSending] = useState(false)
  const [verifying, setVerifying] = useState(false)
  const [localSubmitting, setLocalSubmitting] = useState(false)
  const authRequest = useRef(false)
  const returnTo = safeReturnTo(
    typeof location.state === 'object' && location.state && 'returnTo' in location.state
      ? location.state.returnTo
      : undefined,
  ) ?? '/'

  async function loadCaptcha(expectedEpoch?: number) {
    setCaptchaLoading(true)
    setCaptcha(undefined)
    setSliderX(0)
    try {
      const challenge = await api.createCaptchaChallenge()
      if (expectedEpoch === undefined || authStore.getEpoch() === expectedEpoch) setCaptcha(challenge)
    } catch (error) {
      if (expectedEpoch === undefined || authStore.getEpoch() === expectedEpoch) setSMSError(error)
    } finally {
      if (expectedEpoch === undefined || authStore.getEpoch() === expectedEpoch) setCaptchaLoading(false)
    }
  }

  useEffect(() => {
    let active = true
    api.createCaptchaChallenge().then(
      (challenge) => {
        if (active) {
          setCaptcha(challenge)
          setCaptchaLoading(false)
        }
      },
      (error) => {
        if (active) {
          setSMSError(error)
          setCaptchaLoading(false)
        }
      },
    )
    return () => { active = false }
  }, [])

  useEffect(() => {
    if (retryAfter <= 0) return
    const timer = window.setInterval(() => setRetryAfter((value) => Math.max(0, value - 1)), 1000)
    return () => window.clearInterval(timer)
  }, [retryAfter])

  if (auth.status === 'authenticated') return <Navigate to={returnTo} replace />

  async function sendCode() {
    let phone: string
    try {
      phone = (await smsForm.validateFields(['phone'])).phone
    } catch {
      return
    }
    if (!captcha) return
    setSending(true)
    setSMSError(undefined)
    try {
      const challenge = await api.createSMSChallenge({
        phone,
        captcha_challenge_id: captcha.challenge_id,
        captcha_x: sliderX,
        captcha_y: captcha.tile_y,
      })
      setSMSChallenge(challenge)
      setRetryAfter(challenge.retry_after_seconds)
    } catch (error) {
      setSMSError(error)
      await loadCaptcha()
    } finally {
      setSending(false)
    }
  }

  async function verifyCode(values: SMSLoginValues) {
    if (!smsChallenge || authRequest.current) return
    authRequest.current = true
    const epoch = authStore.beginTransition()
    setVerifying(true)
    setSMSError(undefined)
    try {
      const session = await api.verifySMSChallenge(smsChallenge.challenge_id, values.code)
      if (authStore.setSession(session, epoch)) navigate(returnTo, { replace: true })
    } catch (error) {
      if (authStore.getEpoch() === epoch) {
        setSMSError(error)
        setSMSChallenge(undefined)
        setRetryAfter(0)
        smsForm.setFieldValue('code', '')
        await loadCaptcha(epoch)
      }
    } finally {
      setVerifying(false)
      authRequest.current = false
    }
  }

  async function localLogin(values: LocalLoginValues) {
    if (authRequest.current) return
    authRequest.current = true
    const epoch = authStore.beginTransition()
    setLocalSubmitting(true)
    setLocalError(undefined)
    try {
      const session = await api.localLogin(values.username, values.password)
      if (authStore.setSession(session, epoch)) navigate(returnTo, { replace: true })
    } catch (error) {
      if (authStore.getEpoch() === epoch) setLocalError(error)
    } finally {
      setLocalSubmitting(false)
      authRequest.current = false
    }
  }

  const sliderMax = captchaWidth - captchaTileSize

  async function resendCode() {
    setSMSChallenge(undefined)
    smsForm.setFieldValue('code', '')
    await loadCaptcha()
  }

  return (
    <main className="login-page" id="main-content">
      <section className="login-intro" aria-labelledby="login-title">
        <Text className="eyebrow">企业内容中台</Text>
        <Title id="login-title">回到内容工作的现场</Title>
        <Paragraph>使用大陆手机号安全登录。一次验证，即可继续上次未完成的内容工作。</Paragraph>
        <div className="login-index" aria-hidden="true">01</div>
      </section>

      <section className="login-actions" aria-label="登录方式">
        <div>
          <Title level={2}>手机号登录</Title>
          <Paragraph type="secondary">验证码仅用于本次登录，请勿转发给他人</Paragraph>
        </div>
        <Form<SMSLoginValues> form={smsForm} layout="vertical" requiredMark={false} onFinish={verifyCode}>
          <Form.Item label="手机号" name="phone" rules={[
            { required: true, message: '请输入手机号' },
            { pattern: mainlandPhonePattern, message: '请输入有效的大陆手机号' },
          ]}>
            <Input inputMode="tel" autoComplete="tel-national" maxLength={11} prefix="+86" disabled={Boolean(smsChallenge)} />
          </Form.Item>

          {!smsChallenge ? (
            <div className="captcha-panel" aria-busy={captchaLoading}>
              <div className="captcha-heading">
                <Text strong>安全验证</Text>
                <Button type="link" size="small" loading={captchaLoading} onClick={() => void loadCaptcha()}>换一张</Button>
              </div>
              {captcha ? (
                <>
                  <div className="captcha-stage" style={{ aspectRatio: `${captchaWidth} / ${captchaHeight}` }}>
                    <img src={captcha.background_image} alt="滑动拼图背景" draggable={false} />
                    <img
                      className="captcha-piece"
                      src={captcha.tile_image}
                      alt=""
                      draggable={false}
                      style={{
                        left: `${sliderX / captchaWidth * 100}%`,
                        top: `${captcha.tile_y / captchaHeight * 100}%`,
                        width: `${captchaTileSize / captchaWidth * 100}%`,
                        height: `${captchaTileSize / captchaHeight * 100}%`,
                      }}
                    />
                  </div>
                  <label className="captcha-slider-label" htmlFor="captcha-slider">拖动滑块，使拼图对齐缺口</label>
                  <input
                    id="captcha-slider"
                    className="captcha-slider"
                    type="range"
                    min={0}
                    max={sliderMax}
                    step={1}
                    value={sliderX}
                    aria-valuetext={`已移动 ${sliderX} 像素`}
                    onChange={(event) => setSliderX(Number(event.target.value))}
                    onKeyDown={(event) => {
                      if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
                      event.preventDefault()
                      setSliderX((value) => Math.min(sliderMax, Math.max(0, value + (event.key === 'ArrowRight' ? 1 : -1))))
                    }}
                  />
                </>
              ) : captchaLoading ? <div className="captcha-placeholder">正在准备拼图…</div> : null}
              <Button type="primary" block loading={sending} disabled={!captcha} onClick={() => void sendCode()}>发送验证码</Button>
            </div>
          ) : (
            <>
              <Alert className="sms-sent" type="success" showIcon title="验证码已发送" description={`已发送至 ${smsChallenge.phone_masked}，有效期至 ${new Date(smsChallenge.expires_at).toLocaleString('zh-CN')}。`} />
              <Form.Item label="短信验证码" name="code" rules={[
                { required: true, message: '请输入短信验证码' },
                { pattern: smsCodePattern, message: '请输入 6 位数字验证码' },
              ]}>
                <Input inputMode="numeric" autoComplete="one-time-code" maxLength={6} />
              </Form.Item>
              <Button type="primary" htmlType="submit" block loading={verifying} disabled={localSubmitting}>登录</Button>
              <Button type="link" block disabled={retryAfter > 0 || captchaLoading} onClick={() => void resendCode()}>
                {retryAfter > 0 ? `${retryAfter} 秒后可重新发送` : '重新发送验证码'}
              </Button>
            </>
          )}
          {smsError ? <RequestError error={smsError} /> : null}
        </Form>

        <Collapse
          ghost
          className="emergency-login"
          items={[{
            key: 'local',
            label: '本地应急登录',
            children: (
              <Form<LocalLoginValues> layout="vertical" requiredMark={false} onFinish={localLogin}>
                <Form.Item label="管理员账号" name="username" rules={[{ required: true, message: '请输入管理员账号' }]}>
                  <Input autoComplete="username" maxLength={128} />
                </Form.Item>
                <Form.Item label="密码" name="password" rules={[{ required: true, message: '请输入密码' }]}>
                  <Input.Password autoComplete="current-password" maxLength={1024} />
                </Form.Item>
                {localError ? <RequestError error={localError} /> : null}
                <Button htmlType="submit" block loading={localSubmitting} disabled={verifying}>本地应急登录</Button>
              </Form>
            ),
          }]}
        />
      </section>
    </main>
  )
}
