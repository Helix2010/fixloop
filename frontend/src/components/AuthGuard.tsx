'use client';

import { useEffect, useState } from 'react';
import { apiFetch } from '@/lib/api';
import type { User, SingleResponse } from '@/lib/types';

interface Props {
  children: (user: User) => React.ReactNode;
}

export default function AuthGuard({ children }: Props) {
  const [user, setUser] = useState<User | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    apiFetch<SingleResponse<User>>('/api/v1/me')
      .then((res) => setUser(res.data))
      .catch((err) => {
        if (err?.status !== 401) setError(err.message ?? 'Unknown error');
      });
  }, []);

  if (error) {
    return (
      <div className="min-h-screen flex items-center justify-center text-red-500">
        {error}
      </div>
    );
  }

  if (!user) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <div className="w-8 h-8 border-4 border-blue-500 border-t-transparent rounded-full animate-spin" />
      </div>
    );
  }

  return <>{children(user)}</>;
}
