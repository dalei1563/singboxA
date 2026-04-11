/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ['./web/templates/**/*.html'],
  darkMode: 'class',
  safelist: [
    // Force include light theme colors
    'bg-light-bg', 'bg-light-card', 'bg-light-border', 'text-light-text', 'text-light-muted',
    'border-light-border',
    // Force include ngui status colors (light mode versions)
    'text-status-running', 'text-status-not-running', 'text-status-checking',
    'bg-status-running', 'bg-status-not-running', 'bg-row-selected', 'bg-row-running', 'bg-row-not-running',
    'border-status-running', 'border-status-not-running',
    // Force include ngui colors
    'text-ngui-blue', 'bg-ngui-blue', 'hover:bg-ngui-blue',
    'text-orange-500', 'bg-orange-500', 'hover:bg-orange-600', 'border-orange-500',
    // Force include state classes
    'border-transparent', 'text-gray-500', 'text-gray-600',
    // Force include other classes
    'flex', 'flex-col', 'flex-1', 'flex-wrap', 'items-center', 'items-baseline', 'justify-between',
    'gap-1', 'gap-1.5', 'gap-2', 'gap-3', 'gap-4',
    'px-2', 'px-3', 'px-4', 'px-6', 'py-0.5', 'py-1', 'py-1.5', 'py-3', 'py-4',
    'mx-auto', 'my-1', 'my-4', 'mb-4', 'mb-6', 'mt-4', 'ml-4', 'mr-4', 'ml-2', 'mr-3',
    'w-full', 'max-w-xs', 'max-w-md',
    'text-xs', 'text-sm', 'text-lg', 'text-xl', 'text-2xl',
    'font-bold', 'font-semibold', 'font-medium',
    'rounded', 'rounded-lg', 'rounded-full',
    'border', 'border-2', 'border-b', 'border-b-2',
    'transition-colors', 'animate-spin', 'animate-slide-in',
    'overflow-hidden', 'overflow-auto', 'overflow-x-auto',
    'sticky', 'top-0',
    'divide-y', 'divide-dark-border',
    'h-screen', 'min-h-screen', 'h-full', 'max-h-screen',
    'w-3', 'w-4', 'w-5', 'w-6', 'w-48', 'w-64', 'w-96', 'w-108',
    'space-y-2', 'space-y-4',
    'grid', 'grid-cols-1', 'grid-cols-2', 'grid-cols-3', 'grid-cols-4',
    'md:grid-cols-2', 'lg:grid-cols-4',
    'z-50', 'z-40',
    'cursor-not-allowed', 'opacity-50', 'opacity-70', 'opacity-75',
    'shadow-lg', 'shadow',
    'whitespace-nowrap', 'truncate', 'line-clamp-1',
    'scrollbar-thin',
    'p-2', 'p-3', 'p-4', 'p-6', 'py-2',
    'absolute', 'relative', 'fixed', 'inset-0',
    'bg-black/50', 'bg-black/70',
    // Dark theme still needed for some elements
    'dark',
    // Text colors
    'text-white', 'text-black', 'text-dark-text', 'text-dark-muted',
    // Background colors
    'bg-dark-bg', 'bg-dark-card', 'bg-dark-border',
    // Border colors
    'border-dark-border',
  ],
  theme: {
    extend: {
      colors: {
        // Light theme colors
        light: {
          bg: '#ffffff',
          card: '#f8fafc',
          border: '#e2e8f0',
          text: '#1e293b',
          muted: '#64748b',
        },
        // Dark theme colors (保留)
        dark: {
          bg: '#0f172a',
          card: '#1e293b',
          border: '#334155',
          text: '#e2e8f0',
          muted: '#94a3b8',
        },
        // ngui style colors
        'ngui-blue': '#506da4',
        'ngui-blue-light': '#a8cff0',
        'ngui-red': 'rgba(255, 69, 58, 0.73)',
        // Status colors (ngui spec)
        'status-running': '#22c55e',
        'status-not-running': '#ef4444',
        'status-checking': '#94a3b8',
        // Table row highlights (light mode)
        'row-selected': '#fed7aa',
        'row-running': 'rgba(34, 197, 94, 0.1)',
        'row-not-running': 'rgba(239, 68, 68, 0.1)',
        // Orange accent for light mode
        'accent-orange': '#f97316',
      },
      width: {
        '96': '24rem',
        '108': '27rem',
      },
      animation: {
        'slide-in': 'slide-in 0.3s ease-out',
      },
      keyframes: {
        'slide-in': {
          '0%': { transform: 'translateX(100%)', opacity: '0' },
          '100%': { transform: 'translateX(0)', opacity: '1' },
        },
      },
    }
  },
  plugins: [],
}
