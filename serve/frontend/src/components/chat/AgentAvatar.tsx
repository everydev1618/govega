import { getAvatar } from '../../lib/avatars'

const avatarSizeClasses: Record<number, string> = {
  5: 'w-5 h-5',
  6: 'w-6 h-6',
  7: 'w-7 h-7',
  12: 'w-12 h-12',
  16: 'w-16 h-16',
}

export function AgentAvatar({ name, displayName, avatar, size = 7 }: { name: string; displayName?: string; avatar?: string; size?: number }) {
  const sizeClass = avatarSizeClasses[size!] || 'w-7 h-7'
  const AvatarSvg = getAvatar(avatar)
  if (AvatarSvg) {
    return (
      <div className={`${sizeClass} rounded-full overflow-hidden flex-shrink-0`}>
        <AvatarSvg className="w-full h-full" />
      </div>
    )
  }
  const label = displayName || name
  return (
    <div className={`${sizeClass} rounded-full bg-primary/20 text-primary flex items-center justify-center flex-shrink-0 ${size === 12 || size === 16 ? 'text-lg' : 'text-xs'} font-semibold`}>
      {label[0]?.toUpperCase()}
    </div>
  )
}

export function UserAvatar({ name, size = 7 }: { name?: string; size?: number } = {}) {
  const sizeClass = avatarSizeClasses[size] || 'w-7 h-7'
  if (name) {
    return (
      <div className={`${sizeClass} rounded-full bg-muted flex items-center justify-center flex-shrink-0 text-xs font-semibold text-muted-foreground`}>
        {name[0]?.toUpperCase()}
      </div>
    )
  }
  return (
    <div className={`${sizeClass} rounded-full bg-muted flex items-center justify-center flex-shrink-0`}>
      <svg className="w-3.5 h-3.5 text-muted-foreground" fill="currentColor" viewBox="0 0 20 20">
        <path d="M10 10a4 4 0 100-8 4 4 0 000 8zm-7 8a7 7 0 1114 0H3z" />
      </svg>
    </div>
  )
}
