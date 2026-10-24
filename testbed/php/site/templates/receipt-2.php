Hello <?php echo $name; ?>,

Thanks for your wholesale order <?php echo $order; ?> for <?php echo $company; ?>.

<?php foreach ( $items as $item ) : ?>
  <?php echo $item[1]; ?> x <?php echo $item[0]; ?>  $<?php echo number_format( $item[2], 2 ); ?>

<?php endforeach; ?>
Order total: $<?php echo number_format( $total, 2 ); ?>


We'll invoice you when it ships.

Bean & Leaf Wholesale
https://shop.example.com/wholesale/
