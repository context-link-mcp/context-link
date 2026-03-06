import React from 'react';

export interface ButtonProps {
  label: string;
  onClick: () => void;
  variant?: 'primary' | 'secondary';
  disabled?: boolean;
}

export class ButtonGroup {
  buttons: ButtonProps[];

  constructor(buttons: ButtonProps[]) {
    this.buttons = buttons;
  }

  getEnabled(): ButtonProps[] {
    return this.buttons.filter(b => !b.disabled);
  }
}

export const Button: React.FC<ButtonProps> = ({ label, onClick, variant = 'primary', disabled }) => {
  return (
    <button
      className={`btn btn-${variant}`}
      onClick={onClick}
      disabled={disabled}
    >
      {label}
    </button>
  );
};

export function createButton(label: string, onClick: () => void): ButtonProps {
  return { label, onClick };
}

export type ButtonVariant = 'primary' | 'secondary' | 'danger';
