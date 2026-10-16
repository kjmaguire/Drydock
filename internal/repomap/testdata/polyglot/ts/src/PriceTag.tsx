import { formatPrice, type PriceOptions } from './format.ts';

export function PriceTag(props: { cents: number; opts: PriceOptions }) {
  return <span class="price">{formatPrice(props.cents, props.opts)}</span>;
}

export const CheckoutPanel = (props: { cents: number; opts: PriceOptions }) => (
  <div>
    <PriceTag cents={props.cents} opts={props.opts} />
  </div>
);
