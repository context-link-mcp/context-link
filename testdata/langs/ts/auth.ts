import { hashPassword, comparePassword } from './crypto';
import { UserRepository } from './repository';

// Type alias for user roles
export type UserRole = 'admin' | 'user' | 'guest';

// Interface for authentication tokens
export interface AuthToken {
  userId: string;
  role: UserRole;
  expiresAt: number;
  issuedAt: number;
}

// User authentication class
export class UserAuth {
  private repo: UserRepository;
  private secretKey: string;

  constructor(repo: UserRepository, secretKey: string) {
    this.repo = repo;
    this.secretKey = secretKey;
  }

  async validateToken(token: string): Promise<AuthToken | null> {
    const decoded = this.decodeToken(token);
    if (!decoded) return null;
    if (decoded.expiresAt < Date.now()) return null;
    return decoded;
  }

  private decodeToken(token: string): AuthToken | null {
    try {
      return JSON.parse(atob(token)) as AuthToken;
    } catch {
      return null;
    }
  }

  async login(email: string, password: string): Promise<AuthToken | null> {
    const user = await this.repo.findByEmail(email);
    if (!user) return null;

    const valid = await comparePassword(password, user.passwordHash);
    if (!valid) return null;

    return this.generateToken(user.id, user.role);
  }

  generateToken(userId: string, role: UserRole): AuthToken {
    return {
      userId,
      role,
      issuedAt: Date.now(),
      expiresAt: Date.now() + 3600000,
    };
  }
}

// Standalone exported function
export function isTokenExpired(token: AuthToken): boolean {
  return token.expiresAt < Date.now();
}

// Arrow function assigned to const
export const createGuestToken = (): AuthToken => {
  return {
    userId: 'guest',
    role: 'guest',
    issuedAt: Date.now(),
    expiresAt: Date.now() + 1800000,
  };
};

// Exported constant
export const TOKEN_EXPIRY_MS = 3600000;
