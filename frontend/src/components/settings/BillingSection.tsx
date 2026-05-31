import { useEffect, useState } from 'react';
import {
  Crown,
  Zap,
  Building2,
  Check,
  ArrowUpRight,
  ExternalLink,
  Loader2,
  AlertCircle,
  Calendar,
  RefreshCw,
  Infinity,
} from 'lucide-react';
import { Card, Badge, Button, Spinner, Alert } from '@/components/design-system';
import { useSubscriptionStore } from '@/store/useSubscriptionStore';
import { PlanChangeModal } from './PlanChangeModal';
import {
  SubscriptionTier,
  SubscriptionStatus,
  formatMonthlyPrice,
  formatLimit,
  getTierDisplayName,
  getStatusDisplayName,
  isUpgrade,
  type Plan,
  type SubscriptionTierType,
} from '@/types/subscription';
import { toast } from '@/store/useToastStore';
import './BillingSection.css';

interface BillingSectionProps {
  autoCheckoutPlan?: string | null;
  onAutoCheckoutHandled?: () => void;
  checkoutSuccess?: boolean;
  onCheckoutSuccessHandled?: () => void;
}

export const BillingSection: React.FC<BillingSectionProps> = ({
  autoCheckoutPlan,
  onAutoCheckoutHandled,
  checkoutSuccess,
  onCheckoutSuccessHandled,
}) => {
  const {
    subscription,
    plans,
    isLoadingSubscription,
    isLoadingPlans,
    isChangingPlan,
    subscriptionError,
    plansError,
    fetchSubscription,
    fetchPlans,
    syncSubscription,
    createCheckout,
    getPortalURL,
    cancelSubscription,
    reactivateSubscription,
  } = useSubscriptionStore();

  const [selectedPlan, setSelectedPlan] = useState<Plan | null>(null);
  const [showPlanModal, setShowPlanModal] = useState(false);
  const [isLoadingPortal, setIsLoadingPortal] = useState(false);
  const [autoCheckoutTriggered, setAutoCheckoutTriggered] = useState(false);
  const [isSyncing, setIsSyncing] = useState(false);

  // Fetch subscription and plans on mount
  useEffect(() => {
    fetchSubscription();
    fetchPlans();
  }, [fetchSubscription, fetchPlans]);

  // Handle auto-checkout from URL params
  useEffect(() => {
    if (autoCheckoutPlan && !autoCheckoutTriggered && plans.length > 0 && subscription) {
      const plan = plans.find(p => p.id === autoCheckoutPlan);
      if (plan && plan.tier !== subscription.tier) {
        setAutoCheckoutTriggered(true);
        handlePlanSelect(plan);
        onAutoCheckoutHandled?.();
      }
    }
  }, [autoCheckoutPlan, plans, subscription, autoCheckoutTriggered, onAutoCheckoutHandled]);

  // Handle checkout success - sync subscription after returning from payment
  useEffect(() => {
    if (!checkoutSuccess) return;

    const syncAfterCheckout = async () => {
      setIsSyncing(true);
      let attempts = 0;
      const maxAttempts = 5;
      const delayMs = 2000; // 2 seconds between attempts

      // Poll for subscription update (webhook may take a moment to process)
      while (attempts < maxAttempts) {
        await syncSubscription();

        // Check if subscription was updated from free tier
        const currentSub = useSubscriptionStore.getState().subscription;
        if (currentSub && currentSub.tier !== SubscriptionTier.FREE) {
          toast.success(
            `Welcome to ${getTierDisplayName(currentSub.tier)}! Your subscription is now active.`
          );
          break;
        }

        attempts++;
        if (attempts < maxAttempts) {
          await new Promise(resolve => setTimeout(resolve, delayMs));
        }
      }

      if (attempts >= maxAttempts) {
        // Subscription might not have been processed yet, but don't show error
        toast.info('Payment received! Your subscription will be updated shortly.');
      }

      setIsSyncing(false);
      onCheckoutSuccessHandled?.();
    };

    syncAfterCheckout();
  }, [checkoutSuccess, syncSubscription, onCheckoutSuccessHandled]);

  const currentTier = subscription?.tier || SubscriptionTier.FREE;
  const currentPlan = plans.find(p => p.tier === currentTier);

  const handlePlanSelect = (plan: Plan) => {
    if (plan.contact_sales) {
      // Open contact form or email
      window.open('mailto:support@claraverse.app?subject=Enterprise%20Plan%20Inquiry', '_blank');
      return;
    }

    if (plan.tier === currentTier) {
      toast.info('You are already on this plan');
      return;
    }

    // For upgrades from free tier, go directly to checkout
    if (currentTier === SubscriptionTier.FREE && isUpgrade(currentTier, plan.tier)) {
      handleDirectCheckout(plan.id);
      return;
    }

    // For other changes, show the modal
    setSelectedPlan(plan);
    setShowPlanModal(true);
  };

  const handleDirectCheckout = async (planId: string) => {
    const checkoutUrl = await createCheckout(planId);
    if (checkoutUrl) {
      window.location.href = checkoutUrl;
    } else {
      toast.error('Failed to create checkout session');
    }
  };

  const handleOpenPortal = async () => {
    setIsLoadingPortal(true);
    try {
      const portalUrl = await getPortalURL();
      if (portalUrl) {
        window.open(portalUrl, '_blank');
      } else {
        toast.error('Failed to open billing portal');
      }
    } finally {
      setIsLoadingPortal(false);
    }
  };

  const handleCancelSubscription = async () => {
    if (
      window.confirm(
        'Are you sure you want to cancel your subscription? You will retain access until the end of your billing period.'
      )
    ) {
      const success = await cancelSubscription();
      if (success) {
        toast.success(
          'Subscription cancelled. You will retain access until the end of your billing period.'
        );
      } else {
        toast.error('Failed to cancel subscription');
      }
    }
  };

  const handleReactivate = async () => {
    const success = await reactivateSubscription();
    if (success) {
      toast.success('Subscription reactivated!');
    } else {
      toast.error('Failed to reactivate subscription');
    }
  };

  const getTierIcon = (tier: SubscriptionTierType) => {
    switch (tier) {
      case SubscriptionTier.FREE:
        return <Zap size={20} />;
      case SubscriptionTier.PRO:
        return <Crown size={20} />;
      case SubscriptionTier.MAX:
        return <Crown size={20} className="max-icon" />;
      case SubscriptionTier.ENTERPRISE:
        return <Building2 size={20} />;
      case SubscriptionTier.LEGACY_UNLIMITED:
        return <Infinity size={20} />;
      default:
        return <Zap size={20} />;
    }
  };

  const getTierBadgeVariant = (
    tier: SubscriptionTierType
  ): 'default' | 'accent' | 'success' | 'warning' => {
    switch (tier) {
      case SubscriptionTier.FREE:
        return 'default';
      case SubscriptionTier.PRO:
        return 'accent';
      case SubscriptionTier.MAX:
        return 'success';
      case SubscriptionTier.ENTERPRISE:
        return 'warning';
      case SubscriptionTier.LEGACY_UNLIMITED:
        return 'success'; // Green for legacy users
      default:
        return 'default';
    }
  };

  // Check if user is on legacy unlimited tier
  const isLegacyUser = currentTier === SubscriptionTier.LEGACY_UNLIMITED;

  if (isLoadingSubscription || isLoadingPlans || isSyncing) {
    return (
      <div className="billing-loading">
        <Spinner size="lg" />
        <p>{isSyncing ? 'Confirming your subscription...' : 'Loading billing information...'}</p>
      </div>
    );
  }

  if (subscriptionError || plansError) {
    return (
      <div className="billing-error">
        <Alert variant="error">
          <AlertCircle size={16} />
          <span>{subscriptionError || plansError}</span>
        </Alert>
        <Button
          onClick={() => {
            fetchSubscription();
            fetchPlans();
          }}
        >
          <RefreshCw size={16} />
          Retry
        </Button>
      </div>
    );
  }

  return (
    <div className="billing-section">
      {/* Header */}
      <header className="billing-header">
        <div className="billing-title-section">
          <h1 className="billing-main-title">Billing</h1>
          <p className="billing-subtitle">Manage your subscription and update your plan.</p>
        </div>
      </header>

      {/* Current Plan Card */}
      <section className="billing-current-plan">
        <h3 className="billing-section-title">Current Plan</h3>
        <Card variant="glass" className="current-plan-card">
          <div className="current-plan-header">
            <div className="current-plan-info">
              <div className="current-plan-icon">{getTierIcon(currentTier)}</div>
              <div className="current-plan-details">
                <h4 className="current-plan-name">{getTierDisplayName(currentTier)}</h4>
                <Badge variant={getTierBadgeVariant(currentTier)}>
                  {getStatusDisplayName(subscription?.status || SubscriptionStatus.ACTIVE)}
                </Badge>
              </div>
            </div>
            <div className="current-plan-price">
              {isLegacyUser ? (
                <span className="plan-price legacy-price">Free forever</span>
              ) : (
                currentPlan && (
                  <span className="plan-price">
                    {formatMonthlyPrice(currentPlan.price_monthly)}
                  </span>
                )
              )}
            </div>
          </div>

          {/* Billing period info */}
          {isLegacyUser ? (
            <div className="billing-period-info legacy-info">
              <Infinity size={14} />
              <span>You have Special Lifetime Access - Since you came first</span>
            </div>
          ) : (
            subscription?.current_period_end &&
            currentTier !== SubscriptionTier.FREE && (
              <div className="billing-period-info">
                <Calendar size={14} />
                <span>
                  {subscription.cancel_at_period_end
                    ? `Access until ${new Date(subscription.current_period_end).toLocaleDateString()}`
                    : `Renews on ${new Date(subscription.current_period_end).toLocaleDateString()}`}
                </span>
              </div>
            )
          )}

          {/* Scheduled change notice */}
          {subscription?.scheduled_tier && (
            <Alert variant="info" className="scheduled-change-alert">
              <span>
                Your plan will change to{' '}
                {getTierDisplayName(subscription.scheduled_tier as SubscriptionTierType)} on{' '}
                {subscription.scheduled_change_at
                  ? new Date(subscription.scheduled_change_at).toLocaleDateString()
                  : 'the end of your billing period'}
              </span>
            </Alert>
          )}

          {/* Actions - hidden for legacy users (no billing to manage) */}
          <div className="current-plan-actions">
            {currentTier !== SubscriptionTier.FREE && !isLegacyUser && (
              <>
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={handleOpenPortal}
                  disabled={isLoadingPortal}
                >
                  {isLoadingPortal ? (
                    <Loader2 size={14} className="animate-spin" />
                  ) : (
                    <ExternalLink size={14} />
                  )}
                  Manage Billing
                </Button>
                {subscription?.cancel_at_period_end ? (
                  <Button
                    variant="primary"
                    size="sm"
                    onClick={handleReactivate}
                    disabled={isChangingPlan}
                  >
                    {isChangingPlan ? (
                      <Loader2 size={14} className="animate-spin" />
                    ) : (
                      <RefreshCw size={14} />
                    )}
                    Reactivate
                  </Button>
                ) : (
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={handleCancelSubscription}
                    disabled={isChangingPlan}
                    className="cancel-btn"
                  >
                    Cancel Subscription
                  </Button>
                )}
              </>
            )}
          </div>
        </Card>
      </section>

      {/* Available Plans */}
      <section className="billing-plans">
        <h3 className="billing-section-title">Available Plans</h3>
        <div className="plans-grid">
          {plans.map(plan => (
            <PlanCard
              key={plan.id}
              plan={plan}
              isCurrentPlan={plan.tier === currentTier}
              currentTier={currentTier}
              onSelect={() => handlePlanSelect(plan)}
              isLoading={isChangingPlan}
            />
          ))}
        </div>
      </section>

      {/* Plan Change Modal */}
      <PlanChangeModal
        isOpen={showPlanModal}
        onClose={() => {
          setShowPlanModal(false);
          setSelectedPlan(null);
        }}
        currentPlan={currentPlan || null}
        targetPlan={selectedPlan}
        subscription={subscription}
      />
    </div>
  );
};

// Plan Card Component
interface PlanCardProps {
  plan: Plan;
  isCurrentPlan: boolean;
  currentTier: SubscriptionTierType;
  onSelect: () => void;
  isLoading: boolean;
}

const PlanCard: React.FC<PlanCardProps> = ({
  plan,
  isCurrentPlan,
  currentTier,
  onSelect,
  isLoading,
}) => {
  const isUpgradeAction = isUpgrade(currentTier, plan.tier);

  const getButtonText = () => {
    if (isCurrentPlan) return 'Current Plan';
    if (plan.contact_sales) return 'Contact Sales';
    if (isUpgradeAction) return 'Upgrade';
    return 'Downgrade';
  };

  const getButtonVariant = (): 'primary' | 'secondary' | 'outline' => {
    if (isCurrentPlan) return 'secondary';
    if (plan.contact_sales) return 'outline';
    if (isUpgradeAction) return 'primary';
    return 'secondary';
  };

  return (
    <Card
      variant="glass"
      className={`plan-card ${isCurrentPlan ? 'plan-card-current' : ''} ${plan.tier === SubscriptionTier.PRO ? 'plan-card-popular' : ''}`}
    >
      <div className="plan-card-header">
        <h4 className="plan-card-name">{plan.name}</h4>
        <div className="plan-card-price">
          <span className="plan-price-amount">{formatMonthlyPrice(plan.price_monthly)}</span>
        </div>
      </div>

      <ul className="plan-features">
        {plan.features.map((feature, idx) => (
          <li key={idx} className="plan-feature">
            <Check size={14} className="feature-check" />
            <span>{feature}</span>
          </li>
        ))}
      </ul>

      <div className="plan-limits-summary">
        <div className="limit-item">
          <span className="limit-name">Schedules</span>
          <span className="limit-val">{formatLimit(plan.limits.maxSchedules)}</span>
        </div>
        <div className="limit-item">
          <span className="limit-name">API Keys</span>
          <span className="limit-val">{formatLimit(plan.limits.maxApiKeys)}</span>
        </div>
        <div className="limit-item">
          <span className="limit-name">Executions/day</span>
          <span className="limit-val">{formatLimit(plan.limits.maxExecutionsPerDay)}</span>
        </div>
      </div>

      <Button
        variant={getButtonVariant()}
        size="md"
        onClick={onSelect}
        disabled={isCurrentPlan || isLoading}
        className="plan-select-btn"
      >
        {isLoading && !isCurrentPlan ? (
          <Loader2 size={14} className="animate-spin" />
        ) : plan.contact_sales ? (
          <ArrowUpRight size={14} />
        ) : null}
        {getButtonText()}
      </Button>
    </Card>
  );
};

export default BillingSection;
