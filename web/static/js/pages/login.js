import React from 'react';
import { api } from '../api.js';
import { cn, toast, Button } from '../components/utils.js';
const { createElement: h, useState, useEffect } = React;

export function LoginPage({ onLogin }) {
  const [token, setToken] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const handleSubmit = async (e) => {
    if (e) e.preventDefault();
    if (!token.trim()) return;
    
    setLoading(true);
    setError('');
    
    try {
      await api.loginToken(token.trim());
      // 保存到 localStorage
      localStorage.setItem('auth_token', token.trim());
      toast.success('登录成功');
      if (onLogin) onLogin(token.trim());
    } catch (e) {
      setError('Token 无效，请检查后重试');
      toast.error('登录失败');
    } finally {
      setLoading(false);
    }
  };

  return h('div', { className: 'min-h-screen flex items-center justify-center bg-slate-950' },
    h('div', { className: 'bg-slate-800/50 border border-slate-700/50 rounded-2xl p-8 w-full max-w-sm space-y-6' },
      // Logo / Title
      h('div', { className: 'text-center space-y-2' },
        h('div', { className: 'text-4xl' }, '📺'),
        h('h1', { className: 'text-xl font-bold text-slate-200' }, 'Video Subscribe DL'),
        h('p', { className: 'text-sm text-slate-500' }, '请输入认证 Token 登录')
      ),

      // Token 输入
      h('form', { onSubmit: handleSubmit, className: 'space-y-4' },
        h('div', null,
          h('input', {
            type: 'password',
            value: token,
            onChange: (e) => { setToken(e.target.value); setError(''); },
            placeholder: '输入 Auth Token...',
            autoFocus: true,
            className: cn(
              'w-full bg-slate-900 border rounded-lg px-4 py-3 text-sm text-slate-200 placeholder-slate-600 focus:outline-none transition-colors',
              error ? 'border-red-500/50 focus:border-red-500' : 'border-slate-700 focus:border-blue-500'
            )
          }),
          error && h('p', { className: 'text-xs text-red-400 mt-1.5 ml-1' }, error)
        ),
        h('button', {
          type: 'submit',
          disabled: loading || !token.trim(),
          className: cn(
            'w-full py-3 rounded-lg text-sm font-medium transition-all',
            loading || !token.trim()
              ? 'bg-slate-700 text-slate-500 cursor-not-allowed'
              : 'bg-blue-600 text-white hover:bg-blue-500 active:bg-blue-700'
          )
        }, loading ? '验证中...' : '登录')
      ),

      // 帮助信息
      h('div', { className: 'text-center space-y-1' },
        h('p', { className: 'text-xs text-slate-600' }, 'Token 在首次启动时自动生成，请查看服务日志'),
        h('p', { className: 'text-xs text-slate-600' }, '设置 NO_AUTH=1 环境变量可禁用认证')
      )
    )
  );
}
