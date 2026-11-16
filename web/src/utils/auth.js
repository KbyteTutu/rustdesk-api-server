const TokenKey = 'access_token'
const OidcCode = 'oidc_code'
const OidcCodeExpiry = 'oidc_code_expiry';

export function getToken () {
  return localStorage.getItem(TokenKey)
}

export function setToken (token) {
  localStorage.setItem(`wc-option:local:access_token`, token)
  return localStorage.setItem(TokenKey, token)
}

export function removeToken () {
  for (const key of [TokenKey, 'wc-option:local:access_token', 'user_info']) {
    localStorage.removeItem(key)
  }
  removeCode()
}

// 设置 code，并存储当前时间戳（单位：毫秒）
export function setCode(code) {
  const now = Date.now(); // 当前时间戳（毫秒）
  const expiry = now + 60 * 1000; // 60 秒后过期

  localStorage.setItem(OidcCode, code); // 存储 code
  localStorage.setItem(OidcCodeExpiry, expiry); // 存储过期时间戳
}

// 无效或到期的本地状态不能重新进入登录轮询。
export function getCode() {
  const code = localStorage.getItem(OidcCode);
  const rawExpiry = localStorage.getItem(OidcCodeExpiry);
  const expiry = Number(rawExpiry);

  if (!code || !/^[1-9][0-9]*$/.test(rawExpiry ?? '') ||
      !Number.isSafeInteger(expiry) || expiry <= Date.now()) {
    removeCode();
    return null;
  }
  return code;
}

// 删除 code 和过期时间
export function removeCode() {
  localStorage.removeItem(OidcCode);
  localStorage.removeItem(OidcCodeExpiry);
}
